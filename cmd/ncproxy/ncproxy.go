package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"

	"github.com/Microsoft/go-winio"
	"github.com/Microsoft/hcsshim/cmd/ncproxy/ncproxygrpc"
	"github.com/Microsoft/hcsshim/cmd/ncproxy/nodenetsvc"
	"github.com/Microsoft/hcsshim/hcn"
	"github.com/Microsoft/hcsshim/internal/computeagent"
	"github.com/Microsoft/hcsshim/internal/log"
	"github.com/Microsoft/hcsshim/internal/ncproxyttrpc"
	"github.com/containerd/ttrpc"
	"github.com/containerd/typeurl"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GRPC service exposed for use by a Node Network Service. Holds a mutex for
// updating global client.
type grpcService struct {
	m sync.Mutex
}

var _ ncproxygrpc.NetworkConfigProxyServer = &grpcService{}

func (s *grpcService) AddNIC(ctx context.Context, req *ncproxygrpc.AddNICRequest) (*ncproxygrpc.AddNICResponse, error) {
	log.G(ctx).WithFields(logrus.Fields{
		"containerID":  req.ContainerID,
		"endpointName": req.EndpointName,
		"nicID":        req.NicID,
	}).Info("AddNIC request")

	if req.ContainerID == "" || req.EndpointName == "" || req.NicID == "" {
		return nil, status.Errorf(codes.InvalidArgument, "received empty field in request: %+v", req)
	}
	if client, ok := containerIDToShim[req.ContainerID]; ok {
		caReq := &computeagent.AddNICInternalRequest{
			ContainerID:  req.ContainerID,
			NicID:        req.NicID,
			EndpointName: req.EndpointName,
		}
		if _, err := client.AddNIC(ctx, caReq); err != nil {
			return nil, err
		}
		return &ncproxygrpc.AddNICResponse{}, nil
	}
	return nil, status.Errorf(codes.FailedPrecondition, "No shim registered for namespace `%s`", req.ContainerID)
}

func (s *grpcService) ModifyNIC(ctx context.Context, req *ncproxygrpc.ModifyNICRequest) (*ncproxygrpc.ModifyNICResponse, error) {
	log.G(ctx).WithFields(logrus.Fields{
		"containerID":  req.ContainerID,
		"endpointName": req.EndpointName,
		"nicID":        req.NicID,
	}).Info("ModifyNIC request")

	if req.ContainerID == "" || req.EndpointName == "" || req.NicID == "" {
		return nil, status.Error(codes.InvalidArgument, "received empty field in request")
	}
	if client, ok := containerIDToShim[req.ContainerID]; ok {
		// Changing of the offload weight needs to be ordered correctly. If it's being turned off,
		// we first need to call HCS to change the offload weight and then we need to call HNS to revoke the policy.
		// Whereas if we're enabling it, the ordering is reversed.
		caReq := &computeagent.ModifyNICInternalRequest{
			NicID:        req.NicID,
			EndpointName: req.EndpointName,
			IovWeight:    req.IovWeight,
		}

		iov := hcn.IovPolicySetting{
			IovOffloadWeight:    req.IovWeight,
			QueuePairsRequested: 1,
			InterruptModeration: hcn.IovInterruptModerationMedium,
		}
		rawJSON, err := json.Marshal(iov)
		if err != nil {
			return nil, err
		}

		iovPolicy := hcn.EndpointPolicy{
			Type:     hcn.IOV,
			Settings: rawJSON,
		}
		policies := []hcn.EndpointPolicy{iovPolicy}

		ep, err := hcn.GetEndpointByName(req.EndpointName)
		if err != nil {
			if _, ok := err.(hcn.EndpointNotFoundError); ok {
				return nil, status.Errorf(codes.NotFound, "no endpoint with name `%s` found", req.EndpointName)
			}
			return nil, fmt.Errorf("failed to get endpoint with name `%s`: %s", req.EndpointName, err)
		}

		log.G(ctx).WithFields(logrus.Fields{
			"caReq":     caReq,
			"policies":  policies,
			"iovweight": req.IovWeight,
			"iov":       iov,
			"rawIOV":    rawJSON,
		}).Info("ModifyNIC request parameters")

		if req.IovWeight == 0 {
			if _, err := client.ModifyNIC(ctx, caReq); err != nil {
				return nil, err
			}
			if err := modifyEndpoint(ctx, ep.Id, policies, hcn.RequestTypeUpdate); err != nil {
				return nil, errors.Wrap(err, "failed to modify network adapter")
			}
			if err := modifyEndpoint(ctx, ep.Id, policies, hcn.RequestTypeRemove); err != nil {
				return nil, errors.Wrap(err, "failed to modify network adapter")
			}
		} else {
			if err := modifyEndpoint(ctx, ep.Id, policies, hcn.RequestTypeUpdate); err != nil {
				return nil, errors.Wrap(err, "failed to modify network adapter")
			}
			if _, err := client.ModifyNIC(ctx, caReq); err != nil {
				return nil, err
			}
		}

		return &ncproxygrpc.ModifyNICResponse{}, nil
	}
	return nil, status.Errorf(codes.FailedPrecondition, "No shim registered for namespace `%s`", req.ContainerID)
}

func (s *grpcService) DeleteNIC(ctx context.Context, req *ncproxygrpc.DeleteNICRequest) (*ncproxygrpc.DeleteNICResponse, error) {
	log.G(ctx).WithFields(logrus.Fields{
		"containerID":  req.ContainerID,
		"nicID":        req.NicID,
		"endpointName": req.EndpointName,
	}).Info("DeleteNIC request")

	if req.ContainerID == "" || req.EndpointName == "" || req.NicID == "" {
		return nil, status.Errorf(codes.InvalidArgument, "received empty field in request: %+v", req)
	}
	if client, ok := containerIDToShim[req.ContainerID]; ok {
		caReq := &computeagent.DeleteNICInternalRequest{
			ContainerID:  req.ContainerID,
			NicID:        req.NicID,
			EndpointName: req.EndpointName,
		}
		if _, err := client.DeleteNIC(ctx, caReq); err != nil {
			return nil, err
		}
		return &ncproxygrpc.DeleteNICResponse{}, nil
	}
	return nil, status.Errorf(codes.FailedPrecondition, "No shim registered for namespace `%s`", req.ContainerID)
}

//
// HNS Methods
//
func (s *grpcService) CreateNetwork(ctx context.Context, req *ncproxygrpc.CreateNetworkRequest) (*ncproxygrpc.CreateNetworkResponse, error) {
	log.G(ctx).WithFields(logrus.Fields{
		"networkName": req.Name,
		"type":        req.Mode.String(),
		"ipamType":    req.IpamType,
	}).Info("CreateNetwork request")

	if req.Name == "" || req.Mode.String() == "" || req.IpamType.String() == "" || req.SwitchName == "" {
		return nil, status.Errorf(codes.InvalidArgument, "received empty field in request: %+v", req)
	}

	// Check if the network already exists, and if so return error.
	_, err := hcn.GetNetworkByName(req.Name)
	if err == nil {
		return nil, status.Errorf(codes.FailedPrecondition, "network with name %q already exists", req.Name)
	}

	// Get the layer ID from the external switch. HNS will create a transparent network for
	// any external switch that is created not through HNS so this is what we're
	// searching for here. If the network exists, the vSwitch with this name exists.
	extSwitch, err := hcn.GetNetworkByName(req.SwitchName)
	if err != nil {
		if _, ok := err.(hcn.NetworkNotFoundError); ok {
			return nil, status.Errorf(codes.NotFound, "no network/switch with name `%s` found", req.SwitchName)
		}
		return nil, errors.Wrapf(err, "failed to get network/switch with name %q", req.SwitchName)
	}

	// Get layer ID and use this as the basis for what to layer the new network over.
	if extSwitch.Health.Extra.LayeredOn == "" {
		return nil, status.Errorf(codes.NotFound, "no layer ID found for network %q found", extSwitch.Id)
	}

	layerPolicy := hcn.LayerConstraintNetworkPolicySetting{LayerId: extSwitch.Health.Extra.LayeredOn}
	data, err := json.Marshal(layerPolicy)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal layer policy")
	}

	netPolicy := hcn.NetworkPolicy{
		Type:     hcn.LayerConstraint,
		Settings: data,
	}

	subnets := make([]hcn.Subnet, len(req.SubnetIpadressPrefix))
	for i, addrPrefix := range req.SubnetIpadressPrefix {
		subnet := hcn.Subnet{
			IpAddressPrefix: addrPrefix,
			Routes: []hcn.Route{
				{
					NextHop:           req.DefaultGateway,
					DestinationPrefix: "0.0.0.0/0",
				},
			},
		}
		subnets[i] = subnet
	}

	ipam := hcn.Ipam{
		Type:    req.IpamType.String(),
		Subnets: subnets,
	}

	network := &hcn.HostComputeNetwork{
		Name:     req.Name,
		Type:     hcn.NetworkType(req.Mode.String()),
		Ipams:    []hcn.Ipam{ipam},
		Policies: []hcn.NetworkPolicy{netPolicy},
		SchemaVersion: hcn.SchemaVersion{
			Major: 2,
			Minor: 2,
		},
	}

	network, err = network.Create()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create HNS network %q", req.Name)
	}

	return &ncproxygrpc.CreateNetworkResponse{
		ID: network.Id,
	}, nil
}

func (s *grpcService) CreateEndpoint(ctx context.Context, req *ncproxygrpc.CreateEndpointRequest) (*ncproxygrpc.CreateEndpointResponse, error) {
	log.G(ctx).WithFields(logrus.Fields{
		"endpointName": req.Name,
		"ipAddr":       req.Ipaddress,
		"macAddr":      req.Macaddress,
		"networkName":  req.NetworkName,
	}).Info("CreateEndpoint request")

	if req.Name == "" || req.Ipaddress == "" || req.Macaddress == "" || req.NetworkName == "" {
		return nil, status.Errorf(codes.InvalidArgument, "received empty field in request: %+v", req)
	}

	network, err := hcn.GetNetworkByName(req.NetworkName)
	if err != nil {
		if _, ok := err.(hcn.NetworkNotFoundError); ok {
			return nil, status.Errorf(codes.NotFound, "no network with name `%s` found", req.NetworkName)
		}
		return nil, errors.Wrapf(err, "failed to get network with name %q", req.NetworkName)
	}

	prefixLen, err := strconv.ParseUint(req.IpaddressPrefixlength, 10, 8)
	if err != nil {
		return nil, errors.Wrap(err, "failed to convert ip address prefix length to uint")
	}

	// Construct ip config.
	ipConfig := hcn.IpConfig{
		IpAddress:    req.Ipaddress,
		PrefixLength: uint8(prefixLen),
	}

	// Construct the policy we'll be setting on the endpoint.
	policySettings, err := typeurl.UnmarshalAny(req.PolicySettings)
	if err != nil {
		return nil, err
	}
	policySettingsJSON, err := json.Marshal(policySettings)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal portname")
	}

	// Construct endpoint policy
	epPolicy := hcn.EndpointPolicy{
		Type:     hcn.EndpointPolicyType(req.PolicyType.String()),
		Settings: policySettingsJSON,
	}

	endpoint := &hcn.HostComputeEndpoint{
		Name:               req.Name,
		HostComputeNetwork: network.Id,
		MacAddress:         req.Macaddress,
		IpConfigurations:   []hcn.IpConfig{ipConfig},
		Policies:           []hcn.EndpointPolicy{epPolicy},
		SchemaVersion: hcn.SchemaVersion{
			Major: 2,
			Minor: 0,
		},
	}

	endpoint, err = endpoint.Create()
	if err != nil {
		return nil, errors.Wrap(err, "failed to create HNS endpoint")
	}

	return &ncproxygrpc.CreateEndpointResponse{
		ID: endpoint.Id,
	}, nil
}

func (s *grpcService) AddEndpoint(ctx context.Context, req *ncproxygrpc.AddEndpointRequest) (*ncproxygrpc.AddEndpointResponse, error) {
	log.G(ctx).WithFields(logrus.Fields{
		"endpointName": req.Name,
		"namespaceID":  req.NamespaceID,
	}).Info("AddEndpoint request")

	if req.Name == "" {
		return nil, status.Errorf(codes.InvalidArgument, "received empty field in request: %+v", req)
	}

	ep, err := hcn.GetEndpointByName(req.Name)
	if err != nil {
		if _, ok := err.(hcn.EndpointNotFoundError); ok {
			return nil, status.Errorf(codes.NotFound, "no endpoint with name `%s` found", req.Name)
		}
		return nil, errors.Wrapf(err, "failed to get endpoint with name %q", req.Name)
	}

	if err := hcn.AddNamespaceEndpoint(req.NamespaceID, ep.Id); err != nil {
		return nil, errors.Wrapf(err, "failed to add endpoint with name %q to namespace", req.Name)
	}
	return &ncproxygrpc.AddEndpointResponse{}, nil
}

func (s *grpcService) DeleteEndpoint(ctx context.Context, req *ncproxygrpc.DeleteEndpointRequest) (*ncproxygrpc.DeleteEndpointResponse, error) {
	log.G(ctx).WithFields(logrus.Fields{
		"endpointName": req.Name,
	}).Info("DeleteEndpoint request")

	if req.Name == "" {
		return nil, status.Errorf(codes.InvalidArgument, "received empty field in request: %+v", req)
	}

	ep, err := hcn.GetEndpointByName(req.Name)
	if err != nil {
		if _, ok := err.(hcn.EndpointNotFoundError); ok {
			return nil, status.Errorf(codes.NotFound, "no endpoint with name `%s` found", req.Name)
		}
		return nil, errors.Wrapf(err, "failed to get endpoint with name %q", req.Name)
	}

	if err = ep.Delete(); err != nil {
		return nil, errors.Wrapf(err, "failed to delete endpoint with name %q", req.Name)
	}
	return &ncproxygrpc.DeleteEndpointResponse{}, nil
}

func (s *grpcService) DeleteNetwork(ctx context.Context, req *ncproxygrpc.DeleteNetworkRequest) (*ncproxygrpc.DeleteNetworkResponse, error) {
	log.G(ctx).WithFields(logrus.Fields{
		"networkName": req.Name,
	}).Info("DeleteNetwork request")

	if req.Name == "" {
		return nil, status.Errorf(codes.InvalidArgument, "received empty field in request: %+v", req)
	}

	network, err := hcn.GetNetworkByName(req.Name)
	if err != nil {
		if _, ok := err.(hcn.NetworkNotFoundError); ok {
			return nil, status.Errorf(codes.NotFound, "no network with name `%s` found", req.Name)
		}
		return nil, errors.Wrapf(err, "failed to get network with name %q", req.Name)
	}

	if err = network.Delete(); err != nil {
		return nil, errors.Wrapf(err, "failed to delete network with name %q", req.Name)
	}
	return &ncproxygrpc.DeleteNetworkResponse{}, nil
}

func (s *grpcService) GetEndpoint(ctx context.Context, req *ncproxygrpc.GetEndpointRequest) (*ncproxygrpc.GetEndpointResponse, error) {
	log.G(ctx).WithFields(logrus.Fields{
		"endpointName": req.Name,
	}).Info("GetEndpoint request")

	if req.Name == "" {
		return nil, status.Errorf(codes.InvalidArgument, "received empty field in request: %+v", req)
	}

	ep, err := hcn.GetEndpointByName(req.Name)
	if err != nil {
		if _, ok := err.(hcn.EndpointNotFoundError); ok {
			return nil, status.Errorf(codes.NotFound, "no endpoint with name `%s` found", req.Name)
		}
		return nil, errors.Wrapf(err, "failed to get endpoint with name %q", req.Name)
	}

	return &ncproxygrpc.GetEndpointResponse{
		ID:        ep.Id,
		Name:      ep.Name,
		Network:   ep.HostComputeNetwork,
		Namespace: ep.HostComputeNamespace,
	}, nil
}

func (s *grpcService) GetEndpoints(ctx context.Context, req *ncproxygrpc.GetEndpointsRequest) (*ncproxygrpc.GetEndpointsResponse, error) {
	log.G(ctx).Info("GetEndpoints request")

	rawEndpoints, err := hcn.ListEndpoints()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get HNS endpoints")
	}

	endpoints := make([]*ncproxygrpc.GetEndpointResponse, len(rawEndpoints))
	for i, endpoint := range rawEndpoints {
		resp := &ncproxygrpc.GetEndpointResponse{
			ID:        endpoint.Id,
			Name:      endpoint.Name,
			Network:   endpoint.HostComputeNetwork,
			Namespace: endpoint.HostComputeNamespace,
		}
		endpoints[i] = resp
	}
	return &ncproxygrpc.GetEndpointsResponse{
		Endpoints: endpoints,
	}, nil
}

func (s *grpcService) GetNetwork(ctx context.Context, req *ncproxygrpc.GetNetworkRequest) (*ncproxygrpc.GetNetworkResponse, error) {
	log.G(ctx).WithFields(logrus.Fields{
		"networkName": req.Name,
	}).Info("GetNetwork request")

	if req.Name == "" {
		return nil, status.Errorf(codes.InvalidArgument, "received empty field in request: %+v", req)
	}

	network, err := hcn.GetNetworkByName(req.Name)
	if err != nil {
		if _, ok := err.(hcn.NetworkNotFoundError); ok {
			return nil, status.Errorf(codes.NotFound, "no network with name `%s` found", req.Name)
		}
		return nil, errors.Wrapf(err, "failed to get network with name %q", req.Name)
	}

	return &ncproxygrpc.GetNetworkResponse{
		ID:   network.Id,
		Name: network.Name,
	}, nil
}

func (s *grpcService) GetNetworks(ctx context.Context, req *ncproxygrpc.GetNetworksRequest) (*ncproxygrpc.GetNetworksResponse, error) {
	log.G(ctx).Info("GetNetworks request")

	rawNetworks, err := hcn.ListNetworks()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get HNS networks")
	}

	networks := make([]*ncproxygrpc.GetNetworkResponse, len(rawNetworks))
	for i, network := range rawNetworks {
		resp := &ncproxygrpc.GetNetworkResponse{
			ID:   network.Id,
			Name: network.Name,
		}
		networks[i] = resp
	}

	return &ncproxygrpc.GetNetworksResponse{
		Networks: networks,
	}, nil
}

// TTRPC service exposed for use by the shim. Holds a mutex for updating map of
// client connections.
type ttrpcService struct {
	m sync.Mutex
}

func (s *ttrpcService) RegisterComputeAgent(ctx context.Context, req *ncproxyttrpc.RegisterComputeAgentRequest) (*ncproxyttrpc.RegisterComputeAgentResponse, error) {
	log.G(ctx).WithFields(logrus.Fields{
		"containerID":  req.ContainerID,
		"agentAddress": req.AgentAddress,
	}).Info("RegisterComputeAgent request")

	conn, err := winio.DialPipe(req.AgentAddress, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to connect to compute agent service")
	}
	client := ttrpc.NewClient(conn, ttrpc.WithOnClose(func() { conn.Close() }))
	// Add to global client map if connection succeeds. Don't check if there's already a map entry
	// just overwrite as the client may have changed the address of the config agent.
	s.m.Lock()
	defer s.m.Unlock()
	containerIDToShim[req.ContainerID] = computeagent.NewComputeAgentClient(client)
	return &ncproxyttrpc.RegisterComputeAgentResponse{}, nil
}

func (s *ttrpcService) ConfigureNetworking(ctx context.Context, req *ncproxyttrpc.ConfigureNetworkingInternalRequest) (*ncproxyttrpc.ConfigureNetworkingInternalResponse, error) {
	log.G(ctx).WithFields(logrus.Fields{
		"containerID": req.ContainerID,
	}).Info("ConfigureNetworking request")

	if req.ContainerID == "" {
		return nil, status.Error(codes.InvalidArgument, "ContainerID is empty")
	}

	if nodeNetSvcClient == nil {
		return nil, status.Error(codes.FailedPrecondition, "No NodeNetworkService client registered")
	}

	netsvcReq := &nodenetsvc.ConfigureNetworkingRequest{ContainerID: req.ContainerID, RequestType: nodenetsvc.RequestType(req.RequestType)}
	if _, err := nodeNetSvcClient.client.ConfigureNetworking(ctx, netsvcReq); err != nil {
		return nil, err
	}
	return &ncproxyttrpc.ConfigureNetworkingInternalResponse{}, nil
}

func modifyEndpoint(ctx context.Context, id string, policies []hcn.EndpointPolicy, requestType hcn.RequestType) error {
	endpointRequest := hcn.PolicyEndpointRequest{
		Policies: policies,
	}

	settingsJSON, err := json.Marshal(endpointRequest)
	if err != nil {
		return err
	}

	requestMessage := &hcn.ModifyEndpointSettingRequest{
		ResourceType: hcn.EndpointResourceTypePolicy,
		RequestType:  requestType,
		Settings:     settingsJSON,
	}

	log.G(ctx).WithField("request", requestMessage).Info("sending HcnModifyEndpoint request")

	return hcn.ModifyEndpointSettings(id, requestMessage)
}
