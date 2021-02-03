package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Microsoft/go-winio/pkg/guid"
	"github.com/Microsoft/hcsshim/cmd/ncproxy/ncproxygrpc"
	"github.com/Microsoft/hcsshim/cmd/ncproxy/nodenetsvc"
	"github.com/Microsoft/hcsshim/internal/hns"
	"github.com/Microsoft/hcsshim/internal/log"
	"github.com/containerd/typeurl"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
)

// This is a barebones example of an implementation of the network
// config agent service that ncproxy talks to. This is solely used to test and
// will be removed.

const (
	netID       = "ContainerPlat-nat"
	listenAddr  = "127.0.0.1:6668"
	ncProxyAddr = "127.0.0.1:6669"
)

type service struct {
	client ncproxygrpc.NetworkConfigProxyClient
	nicID  string
}

func (s *service) ConfigureContainerNetworking(ctx context.Context, req *nodenetsvc.ConfigureContainerNetworkingRequest) (*nodenetsvc.ConfigureContainerNetworkingResponse, error) {
	// Change NetworkID to NetworkName if this is the preferred method? Easier to
	// debug etc.
	// log.G(ctx).WithFields(logrus.Fields{
	// 	"namespace": req.NetworkNamespace,
	// }).Info("ConnectNamespaceToNetwork request")
	// endpoints, err := hcsoci.GetNamespaceEndpoints(ctx, req.NetworkNamespace)
	// if err != nil {
	// 	return nil, fmt.Errorf("failed to get namespace endpoints: %s", err)
	// }
	// added := false
	// for _, endpoint := range endpoints {
	// 	if endpoint.VirtualNetworkName == netID {
	// 		nicID, err := guid.NewV4()
	// 		if err != nil {
	// 			return nil, fmt.Errorf("failed to create nic GUID: %s", err)
	// 		}
	// 		nsReq := &ncproxygrpc.AddNICRequest{
	// 			ContainerID:  req.ContainerID,
	// 			NicID:        nicID.String(),
	// 			EndpointName: endpoint.Id,
	// 		}
	// 		if _, err := s.client.AddNIC(ctx, nsReq); err != nil {
	// 			return nil, err
	// 		}
	// 		added = true
	// 	}
	// }
	// if !added {
	// 	return nil, errors.New("no endpoints found to add")
	// }
	return &nodenetsvc.ConfigureContainerNetworkingResponse{}, nil
}

func generateMac() (string, error) {
	buf := make([]byte, 6)
	var mac net.HardwareAddr

	_, err := rand.Read(buf)
	if err != nil {
		return "", err
	}

	// set first numbers to 0
	buf[0] = 0

	mac = append(mac, buf[0], buf[1], buf[2], buf[3], buf[4], buf[5])
	macString := mac.String()
	macString = strings.Replace(macString, ":", "-", -1)
	return strings.ToUpper(macString), nil
}

func (s *service) addHelper(ctx context.Context, req *nodenetsvc.ConfigureNetworkingRequest, containerNamespaceID string) (*nodenetsvc.ConfigureNetworkingResponse, error) {
	// for testing purposes, make the endpoint here
	// - create network, create endpoint, add that to the namespace
	networkInfo, err := hns.GetHNSNetworkByName("mlx1secondlayer")
	if err != nil {
		return nil, err
	}

	iovSettings := &ncproxygrpc.IovEndpointPolicySetting{
		IovOffloadWeight:    100,
		QueuePairsRequested: 1,
		InterruptModeration: 200,
	}

	policySettings, err := typeurl.MarshalAny(iovSettings)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal iov settings to create endpoint")
	}

	macAddr, err := generateMac()
	if err != nil {
		return nil, errors.Wrap(err, "failed to create a usable mac address")
	}

	endpointCreateReq := &ncproxygrpc.CreateEndpointRequest{
		Name:                  req.ContainerID + "_endpoint",
		Macaddress:            macAddr,
		Ipaddress:             "192.168.1.31",
		IpaddressPrefixlength: "24",
		NetworkName:           networkInfo.Name,
		PolicyType:            ncproxygrpc.EndpointPolicyType_IOV,
		PolicySettings:        policySettings,
	}
	_, err = s.client.CreateEndpoint(ctx, endpointCreateReq)
	if err != nil {
		return nil, err
	}

	addEndpointReq := &ncproxygrpc.AddEndpointRequest{
		Name:        req.ContainerID + "_endpoint",
		NamespaceID: containerNamespaceID,
	}
	_, err = s.client.AddEndpoint(ctx, addEndpointReq)
	if err != nil {
		return nil, err
	}

	// add endpoints that are in the namespace as NICs
	nicID, err := guid.NewV4()
	if err != nil {
		return nil, fmt.Errorf("failed to create nic GUID: %s", err)
	}
	s.nicID = nicID.String()
	nsReq := &ncproxygrpc.AddNICRequest{
		ContainerID:  req.ContainerID,
		NicID:        nicID.String(),
		EndpointName: req.ContainerID + "_endpoint",
	}
	if _, err := s.client.AddNIC(ctx, nsReq); err != nil {
		return nil, err
	}

	// normal flow:
	// look in cache of containerID to namespaceID
	// get endpoints request, if the endpoint belongs to the namespaceID,
	// call ncproxy for add nic
	// for every endpoint call ncproxy for add nic
	return &nodenetsvc.ConfigureNetworkingResponse{}, nil

}

func (s *service) modifyHelper(ctx context.Context, req *nodenetsvc.ConfigureNetworkingRequest, containerNamespaceID string) (*nodenetsvc.ConfigureNetworkingResponse, error) {
	eReq := &ncproxygrpc.GetEndpointsRequest{}
	resp, err := s.client.GetEndpoints(ctx, eReq)
	if err != nil {
		return nil, err
	}
	for _, endpoint := range resp.Endpoints {
		if endpoint.Namespace == containerNamespaceID {
			// get all endpoints for namespaceID
			// client.ModifyNIC
			req := &ncproxygrpc.ModifyNICRequest{
				ContainerID:  req.ContainerID,
				NicID:        s.nicID,
				EndpointName: endpoint.Name,
				IovWeight:    0,
			}
			if _, err := s.client.ModifyNIC(ctx, req); err != nil {
				return nil, err
			}
		}
	}

	return &nodenetsvc.ConfigureNetworkingResponse{}, nil
}

func (s *service) ConfigureNetworking(ctx context.Context, req *nodenetsvc.ConfigureNetworkingRequest) (*nodenetsvc.ConfigureNetworkingResponse, error) {
	// read temp file to get container's namespace ID
	filename := fmt.Sprintf("C:/ContainerPlatData/%s", req.ContainerID)
	content, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	containerNamespaceID := string(content)

	if req.RequestType == nodenetsvc.RequestType_RequestType_Setup {
		return s.addHelper(ctx, req, containerNamespaceID)
	}
	if req.RequestType == nodenetsvc.RequestType_RequestType_Modify {
		return s.modifyHelper(ctx, req, containerNamespaceID)
	}

	return &nodenetsvc.ConfigureNetworkingResponse{}, nil
}

func (s *service) PingNodeNetworkService(ctx context.Context, req *nodenetsvc.PingNodeNetworkServiceRequest) (*nodenetsvc.PingNodeNetworkServiceResponse, error) {
	return &nodenetsvc.PingNodeNetworkServiceResponse{}, nil
}

func main() {
	ctx := context.Background()

	sigChan := make(chan os.Signal, 1)
	serveErr := make(chan error, 1)
	defer close(serveErr)
	signal.Notify(sigChan, syscall.SIGINT)
	defer signal.Stop(sigChan)

	grpcClient, err := grpc.Dial(
		ncProxyAddr,
		grpc.WithInsecure(),
		// grpc.WithBlock(),
		// grpc.WithTimeout(30*time.Second),
	)
	if err != nil {
		log.G(ctx).WithError(err).Errorf("failed to connect to ncproxy at %s", ncProxyAddr)
		os.Exit(1)
	}
	defer grpcClient.Close()

	log.G(ctx).WithField("addr", ncProxyAddr).Info("connected to ncproxy")
	ncproxyClient := ncproxygrpc.NewNetworkConfigProxyClient(grpcClient)
	service := &service{ncproxyClient, ""}
	server := grpc.NewServer()
	nodenetsvc.RegisterNodeNetworkServiceServer(server, service)

	grpcListener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.G(ctx).WithError(err).Errorf("failed to listen on %s", grpcListener.Addr().String())
		os.Exit(1)
	}

	go func() {
		defer grpcListener.Close()
		if err := server.Serve(grpcListener); err != nil {
			if strings.Contains(err.Error(), "use of closed network connection") {
				serveErr <- nil
			}
			serveErr <- err
		}
	}()

	log.G(ctx).WithField("addr", listenAddr).Info("serving network service agent")

	// Wait for server error or user cancellation.
	select {
	case <-sigChan:
		log.G(ctx).Info("Received interrupt. Closing")
	case err := <-serveErr:
		if err != nil {
			log.G(ctx).WithError(err).Fatal("grpc service failure")
		}
	}

	// Cancel inflight requests and shutdown service
	server.GracefulStop()
}
