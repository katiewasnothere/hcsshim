package main

import (
	context "context"
	"fmt"
	"net"

	"github.com/Microsoft/go-winio"
	"github.com/Microsoft/hcsshim/internal/computeagent"
	"github.com/Microsoft/hcsshim/internal/hns"
	hcsschema "github.com/Microsoft/hcsshim/internal/schema2"
	"github.com/Microsoft/hcsshim/internal/uvm"
	"github.com/Microsoft/hcsshim/pkg/octtrpc"
	"github.com/containerd/ttrpc"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/Microsoft/hcsshim/internal/log"
)

// This file holds the implementation of the Compute Agent service that is exposed for
// external network configuration.

const podComputeAgentAddrFmt = "\\\\.\\pipe\\computeagent-%s"

// computeAgent implements the ComputeAgent ttrpc service for adding and deleting NICs to a
// Utility VM.
type computeAgent struct {
	uvm *uvm.UtilityVM
}

var _ computeagent.ComputeAgentService = &computeAgent{}

// AddNIC will add a NIC to the computeagent services hosting UVM.
func (ca *computeAgent) AddNIC(ctx context.Context, req *computeagent.AddNICInternalRequest) (*computeagent.AddNICInternalResponse, error) {
	log.G(ctx).WithFields(logrus.Fields{
		"containerID": req.ContainerID,
		"endpointID":  req.EndpointName,
		"nicID":       req.NicID,
	}).Info("AddNIC request")

	endpoint, err := hns.GetHNSEndpointByName(req.EndpointName)
	if err != nil {
		return nil, fmt.Errorf("failed to get endpoint with name `%s`: %s", req.EndpointName, err)
	}
	if err := ca.uvm.AddEndpointToNSWithID(ctx, endpoint.Namespace.ID, req.NicID, endpoint); err != nil {
		return nil, err
	}
	return &computeagent.AddNICInternalResponse{}, nil
}

// DeleteNIC will delete a NIC from the computeagent services hosting UVM.
func (ca *computeAgent) DeleteNIC(ctx context.Context, req *computeagent.DeleteNICInternalRequest) (*computeagent.DeleteNICInternalResponse, error) {
	log.G(ctx).WithFields(logrus.Fields{
		"containerID":  req.ContainerID,
		"nicID":        req.NicID,
		"endpointName": req.EndpointName,
	}).Info("DeleteNIC request")

	endpoint, err := hns.GetHNSEndpointByName(req.EndpointName)
	if err != nil {
		return nil, fmt.Errorf("failed to get endpoint with name `%s`: %s", req.EndpointName, err)
	}
	// Make single element slice so we can just re-use the RemoveEndpoints call instead of making a
	// a similar call that just takes in a singular endpoint.
	endpoints := []*hns.HNSEndpoint{endpoint}
	if err := ca.uvm.RemoveEndpointsFromNS(ctx, endpoint.Namespace.ID, endpoints); err != nil {
		return nil, fmt.Errorf("failed to remove endpoint `%s` from namespace `%s`: %s", req.EndpointName, endpoint.Namespace.ID, err)
	}
	return &computeagent.DeleteNICInternalResponse{}, nil
}

// ModifyNIC will modify a NIC from the computeagent services hosting UVM.
func (ca *computeAgent) ModifyNIC(ctx context.Context, req *computeagent.ModifyNICInternalRequest) (*computeagent.ModifyNICInternalResponse, error) {
	log.G(ctx).WithFields(logrus.Fields{
		"nicID":        req.NicID,
		"endpointName": req.EndpointName,
	}).Info("ModifyNIC request")

	endpoint, err := hns.GetHNSEndpointByName(req.EndpointName)
	if err != nil {
		return nil, fmt.Errorf("failed to get endpoint with name `%s`: %s", req.EndpointName, err)
	}

	nic := &hcsschema.NetworkAdapter{
		EndpointId: endpoint.Id,
		MacAddress: endpoint.MacAddress,
		IovSettings: &hcsschema.IovSettings{
			OffloadWeight: &req.IovWeight,
		},
	}

	if err := ca.uvm.UpdateNIC(ctx, req.NicID, nic); err != nil {
		return nil, errors.Wrap(err, "failed to update UVMS network adapter")
	}

	return &computeagent.ModifyNICInternalResponse{}, nil
}

func setupComputeAgent(caAddr string, parent *uvm.UtilityVM) (*ttrpc.Server, net.Listener, error) {
	// Setup compute agent service
	ttrpcListener, err := winio.ListenPipe(caAddr, nil)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed to listen on %s", caAddr)
	}
	s, err := ttrpc.NewServer(ttrpc.WithUnaryServerInterceptor(octtrpc.ServerInterceptor()))
	if err != nil {
		return nil, nil, err
	}
	caService := &computeAgent{parent}
	computeagent.RegisterComputeAgentService(s, caService)
	return s, ttrpcListener, nil
}

func serveComputeAgent(ctx context.Context, server *ttrpc.Server, l net.Listener) {
	log.G(ctx).WithField("address", l.Addr().String()).Info("serving compute agent")

	go func() {
		defer l.Close()
		if err := trapClosedConnErr(server.Serve(ctx, l)); err != nil {
			log.G(ctx).WithError(err).Fatal("compute agent: serve failure")
		}
	}()
}
