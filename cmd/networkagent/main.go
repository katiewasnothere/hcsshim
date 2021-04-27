package main

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Microsoft/go-winio/pkg/guid"
	"github.com/Microsoft/hcsshim/cmd/ncproxy/ncproxygrpc"
	"github.com/Microsoft/hcsshim/cmd/ncproxy/nodenetsvc"
	"github.com/Microsoft/hcsshim/hcn"
	"github.com/Microsoft/hcsshim/internal/log"
	"google.golang.org/grpc"
)

// This is a barebones example of an implementation of the network
// config agent service that ncproxy talks to. This is solely used to test and
// will be removed.

const (
	listenAddr  = "127.0.0.1:6668"
	ncProxyAddr = "127.0.0.1:6669"
)

type service struct {
	client               ncproxygrpc.NetworkConfigProxyClient
	containerToNamespace map[string]string
	endpointToNicID      map[string]string
	deviceIDToNICVF      map[string]string
	containerToNetwork   map[string]string
}

func (s *service) configureIB(ctx context.Context, containerID, namespaceID string) (_ string, _ uint32, err error) {
	// call to configure IB device
	prefixLength := "24"
	_, gatewayIP, midIP, err := generateIPs(prefixLength)
	if err != nil {
		return "", 0, err
	}
	mac, err := generateMAC()
	if err != nil {
		return "", 0, err
	}

	// TODO katiewasnothere
	// - mount drivers onto the UVM (modular version)
	// - install drivers based on the machine type
	// 		- run modprobe on driver names

	hostDeviceID := "PCI\\VEN_15B3&DEV_101B&SUBSYS_000715B3&REV_00\\8&2D455990&0&000000200009"
	var hostDeviceVFIndex uint32 = 0

	// call to configure IB VF
	// - assign VF to UVM
	assignReq := &ncproxygrpc.AssignVFRequest{
		ContainerID: containerID,
		// TODO katiewasnothere: hardcode device and virtual function index for testing
		DeviceID:             hostDeviceID,
		VirtualFunctionIndex: hostDeviceVFIndex,
		DeviceType:           ncproxygrpc.AssignVFRequest_Infiniband,
	}
	assignResp, err := s.client.AssignVF(ctx, assignReq)
	if err != nil {
		return "", 0, err
	}

	log.G(ctx).WithField("resp", assignResp).Info("assigned vf")

	defer func() {
		if err != nil {
			// remove VF
			removeReq := &ncproxygrpc.RemoveVFRequest{
				ContainerID:          containerID,
				DeviceID:             hostDeviceID,
				VirtualFunctionIndex: hostDeviceVFIndex,
				DeviceType:           ncproxygrpc.RemoveVFRequest_Infiniband,
			}
			if _, err := s.client.RemoveVF(ctx, removeReq); err != nil {
				log.G(ctx).WithError(err).Info("Failed to remove VF")
			}
		}
	}()

	addReq := &ncproxygrpc.AddNICVirtualFunctionRequest{
		NamespaceID:           namespaceID,
		ContainerID:           containerID,
		DeviceID:              assignResp.ID,
		Macaddress:            mac,
		Ipaddress:             midIP,
		IpaddressPrefixlength: 24,
		// TODO katiewasnothere: do I need the network name??
		Gateway: gatewayIP,
	}

	_, err = s.client.AddNICVirtualFunction(ctx, addReq)
	if err != nil {
		return "", 0, err
	}

	log.G(ctx).WithField("resp", assignResp).Info("added nic vf")

	// - ccall to add adapter to LCOW
	// 		- relies on fix to opengcs
	// 		- should this call also handle running ifconfig and ip link to configure?
	//			- I'm thinking yes

	// TODO katiewasnothere
	// - get drivers in the container
	// 		- for now maybe I can just mount the same scsi device into the container
	return hostDeviceID, hostDeviceVFIndex, nil
}

// TODO katiewasnothere: maybe we could have a boolean for netsvc or IB
func (s *service) ConfigureContainerNetworking(ctx context.Context, req *nodenetsvc.ConfigureContainerNetworkingRequest) (_ *nodenetsvc.ConfigureContainerNetworkingResponse, err error) {
	// make endpoint and network: katiewasnothere

	// for testing purposes, make the endpoint here
	// - create network, create endpoint, add that to the namespace
	if req.RequestType == nodenetsvc.RequestType_Setup {
		log.G(ctx).WithField("req", req).Info("ConfigureContainreNetworking request")
		prefixLength := "24"
		prefixIP, gatewayIP, midIP, err := generateIPs(prefixLength)
		if err != nil {
			return nil, err
		}

		addNetworkReq := &ncproxygrpc.CreateNetworkRequest{
			Name:                 req.ContainerID + "_network",
			Mode:                 ncproxygrpc.CreateNetworkRequest_Transparent,
			SwitchName:           "mlx",
			IpamType:             ncproxygrpc.CreateNetworkRequest_Static,
			SubnetIpadressPrefix: []string{prefixIP},
			DefaultGateway:       gatewayIP,
		}

		networkResp, err := s.client.CreateNetwork(ctx, addNetworkReq)
		if err != nil {
			return nil, err
		}

		network, err := hcn.GetNetworkByID(networkResp.ID)
		if err != nil {
			return nil, err
		}

		s.containerToNetwork[req.ContainerID] = network.Name

		log.G(ctx).WithField("network", networkResp).Info("ConfigureContainreNetworking created network")

		iovSettings := &ncproxygrpc.IovEndpointPolicySetting{
			IovOffloadWeight:    100,
			QueuePairsRequested: 1,
			InterruptModeration: 200,
		}

		mac, err := generateMAC()
		if err != nil {
			return nil, err
		}

		name := req.ContainerID + "_endpoint"
		endpointCreateReq := &ncproxygrpc.CreateEndpointRequest{
			Name:                  name,
			Macaddress:            mac,
			Ipaddress:             midIP,
			IpaddressPrefixlength: prefixLength,
			NetworkName:           network.Name,
			IovPolicySettings:     iovSettings,
		}

		log.G(ctx).WithField("endpointCreateReq", endpointCreateReq).Info("ConfigureContainreNetworking endpointCreateReq")

		endpt, err := s.client.CreateEndpoint(ctx, endpointCreateReq)
		if err != nil {
			return nil, err
		}

		log.G(ctx).WithField("endpt", endpt).Info("ConfigureContainreNetworking created endpoint")

		addEndpointReq := &ncproxygrpc.AddEndpointRequest{
			Name:        name,
			NamespaceID: req.NetworkNamespaceID,
		}
		_, err = s.client.AddEndpoint(ctx, addEndpointReq)
		if err != nil {
			return nil, err
		}

		log.G(ctx).WithField("endpt", endpt).Info("ConfigureContainreNetworking added endpoint")

		s.containerToNamespace[req.ContainerID] = req.NetworkNamespaceID

		resultIPAddr := &nodenetsvc.ContainerIPAddress{
			Version:        "4",
			Ip:             midIP,
			PrefixLength:   "24",
			DefaultGateway: gatewayIP,
		}
		netInterface := &nodenetsvc.ContainerNetworkInterface{
			Name:               network.Name,
			MacAddress:         mac,
			NetworkNamespaceID: req.NetworkNamespaceID,
			Ipaddresses:        []*nodenetsvc.ContainerIPAddress{resultIPAddr},
		}

		return &nodenetsvc.ConfigureContainerNetworkingResponse{
			Interfaces: []*nodenetsvc.ContainerNetworkInterface{netInterface},
		}, nil
	} else if req.RequestType == nodenetsvc.RequestType_Teardown {
		eReq := &ncproxygrpc.GetEndpointsRequest{}
		resp, err := s.client.GetEndpoints(ctx, eReq)
		if err != nil {
			return nil, err
		}

		for _, endpoint := range resp.Endpoints {
			if endpoint.Namespace == req.NetworkNamespaceID {
				deleteEndptReq := &ncproxygrpc.DeleteEndpointRequest{
					Name: endpoint.Name,
				}
				if _, err := s.client.DeleteEndpoint(ctx, deleteEndptReq); err != nil {
					log.G(ctx).WithField("name", endpoint.Name).Warn("failed to delete endpoint")
					// best effort
					// return err
				}
			}
		}

		if networkName, ok := s.containerToNetwork[req.ContainerID]; ok {
			deleteReq := &ncproxygrpc.DeleteNetworkRequest{
				Name: networkName,
			}
			if _, err := s.client.DeleteNetwork(ctx, deleteReq); err != nil {
				log.G(ctx).WithField("name", networkName).Warn("failed to delete network")
				// best effort
			}
			delete(s.containerToNetwork, req.ContainerID)
		}

		return &nodenetsvc.ConfigureContainerNetworkingResponse{}, nil

	}
	return nil, fmt.Errorf("invalid request type %v", req.RequestType)
}

func generateMAC() (string, error) {
	buf := make([]byte, 6)

	_, err := rand.Read(buf)
	if err != nil {
		return "", err
	}

	// set first number to 0
	buf[0] = 0
	mac := net.HardwareAddr(buf)
	macString := strings.ToUpper(mac.String())
	macString = strings.Replace(macString, ":", "-", -1)

	return macString, nil
}

func generateIPs(prefixLength string) (string, string, string, error) {
	buf := make([]byte, 4)
	_, err := rand.Read(buf)
	if err != nil {
		return "", "", "", err
	}

	// set first to 192
	buf[0] = 192
	buf[1] = 168
	buf[2] = 50
	// set last to 0 for prefix
	buf[3] = 0
	ipPrefix := net.IP(buf)
	ipPrefixString := ipPrefix.String() + "/" + prefixLength

	// set the last to 1 for gateway
	buf[3] = 1
	ipGateway := net.IP(buf)
	ipGatewayString := ipGateway.String()

	// set last to 2 for IP in range
	buf[3] = byte(rand.Intn(255-2) + 2)
	ip := net.IP(buf)
	ipString := ip.String()

	return ipPrefixString, ipGatewayString, ipString, nil
}

func (s *service) addHelper(ctx context.Context, req *nodenetsvc.ConfigureNetworkingRequest, containerNamespaceID string) (_ *nodenetsvc.ConfigureNetworkingResponse, err error) {
	eReq := &ncproxygrpc.GetEndpointsRequest{}
	resp, err := s.client.GetEndpoints(ctx, eReq)
	if err != nil {
		return nil, err
	}
	log.G(ctx).WithField("endpts", resp.Endpoints).Info("ConfigureNetworking addrequest")

	for _, endpoint := range resp.Endpoints {
		if endpoint.Namespace == containerNamespaceID {
			// add endpoints that are in the namespace as NICs
			nicID, err := guid.NewV4()
			if err != nil {
				return nil, fmt.Errorf("failed to create nic GUID: %s", err)
			}
			nsReq := &ncproxygrpc.AddNICRequest{
				ContainerID:  req.ContainerID,
				NicID:        nicID.String(),
				EndpointName: endpoint.Name,
			}
			if _, err := s.client.AddNIC(ctx, nsReq); err != nil {
				return nil, err
			}
			log.G(ctx).WithField("nic add req", nsReq).Info("ConfigureNetworking addrequest nic added")

			s.endpointToNicID[endpoint.Name] = nicID.String()
		}

	}

	defer func() {
		if err != nil {
			_, _ = s.teardownHelper(ctx, req, containerNamespaceID)
		}
	}()

	// test out ib flow katiewasnothere
	_, _, err = s.configureIB(ctx, req.ContainerID, containerNamespaceID)
	if err != nil {
		return nil, err
	}

	// normal flow:
	// look in cache of containerID to namespaceID
	// get endpoints request, if the endpoint belongs to the namespaceID,
	// call ncproxy for add nic
	// for every endpoint call ncproxy for add nic
	return &nodenetsvc.ConfigureNetworkingResponse{}, nil

}

func (s *service) teardownHelper(ctx context.Context, req *nodenetsvc.ConfigureNetworkingRequest, containerNamespaceID string) (*nodenetsvc.ConfigureNetworkingResponse, error) {
	eReq := &ncproxygrpc.GetEndpointsRequest{}
	resp, err := s.client.GetEndpoints(ctx, eReq)
	if err != nil {
		return nil, err
	}
	for _, endpoint := range resp.Endpoints {
		if endpoint.Namespace == containerNamespaceID {
			nicID, ok := s.endpointToNicID[endpoint.Name]
			if !ok {
				log.G(ctx).WithField("name", endpoint.Name).Warn("endpoint was not assigned a NIC ID previously")
				continue
				// best effort
				// return nil, fmt.Errorf("endpoint was not assigned a NIC ID previously")
			}
			// remove endpoints that are in the namespace as NICs
			nsReq := &ncproxygrpc.DeleteNICRequest{
				ContainerID:  req.ContainerID,
				NicID:        nicID,
				EndpointName: endpoint.Name,
			}
			if _, err := s.client.DeleteNIC(ctx, nsReq); err != nil {
				log.G(ctx).WithField("name", endpoint.Name).Warn("failed to delete endpoint nic")
				// best effort
				// return nil, err
			}
			delete(s.endpointToNicID, endpoint.Name)
		}
	}

	// normal flow:
	// look in cache of containerID to namespaceID
	// get endpoints request, if the endpoint belongs to the namespaceID,
	// call ncproxy for add nic
	// for every endpoint call ncproxy for add nic
	return &nodenetsvc.ConfigureNetworkingResponse{}, nil
}

func (s *service) modifyHelper(ctx context.Context, containerID, containerNamespaceID string, iovSettings *ncproxygrpc.IovEndpointPolicySetting) error {
	eReq := &ncproxygrpc.GetEndpointsRequest{}
	resp, err := s.client.GetEndpoints(ctx, eReq)
	if err != nil {
		return err
	}
	for _, endpoint := range resp.Endpoints {
		if endpoint.Namespace == containerNamespaceID {
			nicID, ok := s.endpointToNicID[endpoint.Name]
			if !ok {
				return fmt.Errorf("endpoint was not assigned a NIC ID previously")
			}

			req := &ncproxygrpc.ModifyNICRequest{
				ContainerID:       containerID,
				NicID:             nicID,
				EndpointName:      endpoint.Name,
				IovPolicySettings: iovSettings,
			}
			if _, err := s.client.ModifyNIC(ctx, req); err != nil {
				return err
			}
		}
	}

	return nil
}

func (s *service) ConfigureNetworking(ctx context.Context, req *nodenetsvc.ConfigureNetworkingRequest) (*nodenetsvc.ConfigureNetworkingResponse, error) {
	containerNamespaceID, ok := s.containerToNamespace[req.ContainerID]
	if !ok {
		return nil, fmt.Errorf("no namespace was previously created for containerID %s", req.ContainerID)
	}

	log.G(ctx).WithField("req", req).Info("ConfigureNetworking request")

	if req.RequestType == nodenetsvc.RequestType_Setup {
		return s.addHelper(ctx, req, containerNamespaceID)
	}
	// TODO katiewasnothere: handle teardown req
	return s.teardownHelper(ctx, req, containerNamespaceID)
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
	service := &service{
		client:               ncproxyClient,
		containerToNamespace: make(map[string]string),
		endpointToNicID:      make(map[string]string),
		deviceIDToNICVF:      make(map[string]string),
		containerToNetwork:   make(map[string]string),
	}
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

/*var modifyCommand = cli.Command{
	Name: "modify",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "containerID",
			Usage: "the ID of the container to modify",
		},
		cli.StringFlag{
			Name:  "namespaceID",
			Usage: "the ID of the namespace for the container",
		},
		cli.Uint64Flag{
			Name:  "offload-weight",
			Usage: "target iov offload weight",
		},
		cli.Uint64Flag{
			Name:  "queue-pairs",
			Usage: "target iov queue pairs",
			Value: 1,
		},
		cli.Uint64Flag{
			Name:  "interruption-mode",
			Usage: "target iov interruption mode",
			Value: 200,
		},
	},
	Action: func(cliCtx *cli.Context) error {
		ctx := context.Background()

		containerID := cliCtx.String("containerID")
		if containerID == "" {
			return errors.New("containerID is required to modify settings")
		}

		namespaceID := cliCtx.String("namespaceID")
		if namespaceID == "" {
			return errors.New("namespaceID is required to modify settings")
		}

		grpcClient, err := grpc.Dial(
			ncProxyAddr,
			grpc.WithInsecure(),
		)
		if err != nil {
			log.G(ctx).WithError(err).Errorf("failed to connect to ncproxy at %s", ncProxyAddr)
			os.Exit(1)
		}
		defer grpcClient.Close()

		ncproxyClient := ncproxygrpc.NewNetworkConfigProxyClient(grpcClient)
		service := &service{ncproxyClient, ""}

		iovWeight := cliCtx.Uint64("offload-weight")
		queuePairs := cliCtx.Uint64("queue-pairs")
		interruptMode := cliCtx.Uint64("interruption-mode")

		iovSettings := &ncproxygrpc.IovEndpointPolicySetting{
			IovOffloadWeight:    uint32(iovWeight),
			QueuePairsRequested: uint32(queuePairs),
			InterruptModeration: uint32(interruptMode),
		}

		// TODO katiewasnothere: how to get service
		return service.modifyHelper(ctx, containerID, namespaceID, iovSettings)
	},
}*/
