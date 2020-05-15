package hcsoci

import (
	"context"
	"fmt"

	"github.com/Microsoft/hcsshim/internal/hns"
	"github.com/Microsoft/hcsshim/internal/log"
	"github.com/Microsoft/hcsshim/internal/logfields"
	"github.com/Microsoft/hcsshim/internal/ncproxyttrpc"
	"github.com/Microsoft/hcsshim/internal/resources"
	"github.com/Microsoft/hcsshim/internal/uvm"
	"github.com/sirupsen/logrus"
)

func createNetworkNamespace(ctx context.Context, coi *createOptionsInternal, r *resources.Resources) error {
	op := "hcsoci::createNetworkNamespace"
	l := log.G(ctx).WithField(logfields.ContainerID, coi.ID)
	l.Debug(op + " - Begin")
	defer func() {
		l.Debug(op + " - End")
	}()

	netID, err := hns.CreateNamespace()
	if err != nil {
		return err
	}
	log.G(ctx).WithFields(logrus.Fields{
		"netID":               netID,
		logfields.ContainerID: coi.ID,
	}).Info("created network namespace for container")
	r.SetNetNS(netID)
	r.SetCreatedNetNS(true)
	endpoints := make([]string, 0)
	for _, endpointID := range coi.Spec.Windows.Network.EndpointList {
		err = hns.AddNamespaceEndpoint(netID, endpointID)
		if err != nil {
			return err
		}
		log.G(ctx).WithFields(logrus.Fields{
			"netID":      netID,
			"endpointID": endpointID,
		}).Info("added network endpoint to namespace")
		endpoints = append(endpoints, endpointID)
	}
	r.Add(&uvm.NetworkEndpoints{EndpointIDs: endpoints, Namespace: netID})
	return nil
}

// GetNamespaceEndpoints gets all endpoints in `netNS`
func GetNamespaceEndpoints(ctx context.Context, netNS string) ([]*hns.HNSEndpoint, error) {
	op := "hcsoci::GetNamespaceEndpoints"
	l := log.G(ctx).WithField("netns-id", netNS)
	l.Debug(op + " - Begin")
	defer func() {
		l.Debug(op + " - End")
	}()

	ids, err := hns.GetNamespaceEndpoints(netNS)
	if err != nil {
		return nil, err
	}
	var endpoints []*hns.HNSEndpoint
	for _, id := range ids {
		endpoint, err := hns.GetHNSEndpointByID(id)
		if err != nil {
			return nil, err
		}
		endpoints = append(endpoints, endpoint)
	}
	return endpoints, nil
}

// NetworkSetup is used to abstract away the details of setting up networking
// for a container.
type NetworkSetup interface {
	ConfigureNetworking(ctx context.Context, namespaceID string) error
}

// LocalNetworkSetup implements the NetworkSetup interface for configuring container
// networking.
type LocalNetworkSetup struct {
	VM *uvm.UtilityVM
}

func (l *LocalNetworkSetup) ConfigureNetworking(ctx context.Context, namespaceID string) error {
	endpoints, err := GetNamespaceEndpoints(ctx, namespaceID)
	if err != nil {
		return err
	}
	if err := l.VM.AddNetNS(ctx, namespaceID); err != nil {
		return err
	}
	return l.VM.AddEndpointsToNS(ctx, namespaceID, endpoints)
}

// ExternalNetworkSetup implements the NetworkSetup interface for configuring
// container networking. It will try and communicate with an external network configuration
// proxy service to setup networking.
type ExternalNetworkSetup struct {
	VM          *uvm.UtilityVM
	CaAddr      string
	ContainerID string
}

func (e *ExternalNetworkSetup) ConfigureNetworking(ctx context.Context, namespaceID string) error {
	client := e.VM.NCProxyClient()
	if client == nil {
		return fmt.Errorf("no ncproxy client for UVM %q", e.VM.ID())
	}

	if err := e.VM.AddNetNS(ctx, namespaceID); err != nil {
		return err
	}

	registerReq := &ncproxyttrpc.RegisterComputeAgentRequest{
		ContainerID:  e.ContainerID,
		AgentAddress: e.CaAddr,
	}
	if _, err := client.RegisterComputeAgent(ctx, registerReq); err != nil {
		return err
	}

	netReq := &ncproxyttrpc.ConfigureNetworkingInternalRequest{
		ContainerID: e.ContainerID,
	}
	if _, err := client.ConfigureNetworking(ctx, netReq); err != nil {
		return err
	}

	return nil
}
