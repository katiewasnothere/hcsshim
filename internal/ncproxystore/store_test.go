package ncproxystore

import (
	"context"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/Microsoft/hcsshim/internal/networking"
	bolt "go.etcd.io/bbolt"
)

func TestComputeAgentStore(t *testing.T) {
	ctx := context.Background()
	tempDir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	db, err := bolt.Open(filepath.Join(tempDir, "networkproxy.db.test"), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store := NewComputeAgentStore(db)
	containerID := "fake-container-id"
	address := "123412341234"

	if err := store.UpdateComputeAgent(ctx, containerID, address); err != nil {
		t.Fatal(err)
	}

	actual, err := store.GetComputeAgent(ctx, containerID)
	if err != nil {
		t.Fatal(err)
	}

	if address != actual {
		t.Fatalf("compute agent addresses are not equal, expected %v but got %v", address, actual)
	}

	if err := store.DeleteComputeAgent(ctx, containerID); err != nil {
		t.Fatal(err)
	}

	value, err := store.GetComputeAgent(ctx, containerID)
	if err == nil {
		t.Fatalf("expected an error, instead found value %s", value)
	}
}

func TestComputeAgentStore_GetComputeAgents(t *testing.T) {
	ctx := context.Background()
	tempDir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	db, err := bolt.Open(filepath.Join(tempDir, "networkproxy.db.test"), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store := NewComputeAgentStore(db)

	containerIDs := []string{"fake-container-id", "fake-container-id-2"}
	addresses := []string{"123412341234", "234523452345"}

	target := make(map[string]string)
	for i := 0; i < len(containerIDs); i++ {
		target[containerIDs[i]] = addresses[i]
		if err := store.UpdateComputeAgent(ctx, containerIDs[i], addresses[i]); err != nil {
			t.Fatal(err)
		}
	}

	actual, err := store.GetComputeAgents(ctx)
	if err != nil {
		t.Fatal(err)
	}

	for k, v := range actual {
		if target[k] != v {
			t.Fatalf("expected to get %s for key %s, instead got %s", target[k], k, v)
		}
	}
}

func TestEndpointStore(t *testing.T) {
	ctx := context.Background()
	tempDir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	db, err := bolt.Open(filepath.Join(tempDir, "networkproxy.db.test"), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store := NewEndpointStore(db)
	endpointName := "test-endpoint-name"
	namespaceID := "test-namespace-id"

	endpoint := &networking.CustomEndpoint{
		EndpointName: endpointName,
		NamespaceID:  namespaceID,
	}

	if err := store.Update(ctx, endpointName, endpoint); err != nil {
		t.Fatal(err)
	}

	actual, err := store.Get(ctx, endpointName)
	if err != nil {
		t.Fatal(err)
	}

	if actual.EndpointName != endpointName {
		t.Fatalf("endpoint name is not equal, expected %v but got %v", endpointName, actual.EndpointName)
	}

	if actual.NamespaceID != namespaceID {
		t.Fatalf("endpoint namespace id is not equal, expected %v but got %v", namespaceID, actual.NamespaceID)
	}

	if err := store.Delete(ctx, endpointName); err != nil {
		t.Fatal(err)
	}

	actual, err = store.Get(ctx, endpointName)
	if err == nil {
		t.Fatalf("expected an error, instead found endpoint %s", actual)
	}
}

func TestEndpointStore_GetAll(t *testing.T) {
	ctx := context.Background()
	tempDir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	db, err := bolt.Open(filepath.Join(tempDir, "networkproxy.db.test"), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store := NewEndpointStore(db)

	endpointNames := []string{"endpoint-name-1", "endpoint-name-2"}
	endpoints := []*networking.CustomEndpoint{
		{
			EndpointName: endpointNames[0],
		},
		{
			EndpointName: endpointNames[1],
		},
	}

	target := make(map[string]*networking.CustomEndpoint)
	for i := 0; i < len(endpointNames); i++ {
		target[endpointNames[i]] = endpoints[i]
		if err := store.Update(ctx, endpointNames[i], endpoints[i]); err != nil {
			t.Fatal(err)
		}
	}

	actual, err := store.GetAll(ctx)
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range actual {
		endpt, ok := target[e.EndpointName]
		if !ok {
			t.Fatalf("unexpected endpoint with name %v found", e.EndpointName)
		}
		if endpt.EndpointName != e.EndpointName {
			t.Fatalf("expected found endpoint to have name %v, instead found %v", endpt.EndpointName, e.EndpointName)
		}
	}
}

func TestNetworkStore(t *testing.T) {
	ctx := context.Background()
	tempDir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	db, err := bolt.Open(filepath.Join(tempDir, "networkproxy.db.test"), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store := NewNetworkStore(db)
	networkName := "test-network-name"

	network := &networking.CustomNetwork{
		NetworkName: networkName,
	}

	if err := store.Update(ctx, networkName, network); err != nil {
		t.Fatal(err)
	}

	actual, err := store.Get(ctx, networkName)
	if err != nil {
		t.Fatal(err)
	}

	if actual.NetworkName != networkName {
		t.Fatalf("network name is not equal, expected %v but got %v", networkName, actual.NetworkName)
	}

	if err := store.Delete(ctx, networkName); err != nil {
		t.Fatal(err)
	}

	actual, err = store.Get(ctx, networkName)
	if err == nil {
		t.Fatalf("expected an error, instead found network %s", actual)
	}
}

func TestNetworkStore_GetAll(t *testing.T) {
	ctx := context.Background()
	tempDir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	db, err := bolt.Open(filepath.Join(tempDir, "networkproxy.db.test"), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store := NewNetworkStore(db)

	networkNames := []string{"network-name-1", "network-name-2"}
	networks := []*networking.CustomNetwork{
		{
			NetworkName: networkNames[0],
		},
		{
			NetworkName: networkNames[1],
		},
	}

	target := make(map[string]*networking.CustomNetwork)
	for i := 0; i < len(networkNames); i++ {
		target[networkNames[i]] = networks[i]
		if err := store.Update(ctx, networkNames[i], networks[i]); err != nil {
			t.Fatal(err)
		}
	}

	actual, err := store.GetAll(ctx)
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range actual {
		network, ok := target[n.NetworkName]
		if !ok {
			t.Fatalf("unexpected network with name %v found", n.NetworkName)
		}
		if network.NetworkName != n.NetworkName {
			t.Fatalf("expected found network to have name %v, instead found %v", network.NetworkName, n.NetworkName)
		}
	}
}
