package ncproxystore

import (
	"context"
	"encoding/json"

	"github.com/Microsoft/hcsshim/internal/networking"
	"github.com/pkg/errors"
	bolt "go.etcd.io/bbolt"
)

var (
	ErrBucketNotFound = errors.New("bucket not found")
	errKeyNotFound    = errors.New("key does not exist")
)

type NetworkStore struct {
	DB *bolt.DB
}

func NewNetworkStore(db *bolt.DB) *NetworkStore {
	return &NetworkStore{DB: db}
}

func (n *NetworkStore) Get(ctx context.Context, key string) (*networking.NCProxyNetwork, error) {
	internalData := &networking.NCProxyNetwork{}
	if err := n.DB.View(func(tx *bolt.Tx) error {
		bkt := getNetworkBucket(tx)
		if bkt == nil {
			return errors.Wrapf(ErrBucketNotFound, "network bucket %v", bucketKeyNetwork)
		}
		data := bkt.Get([]byte(key))
		if data == nil {
			return errors.Wrapf(errKeyNotFound, "network %v", key)
		}
		if err := json.Unmarshal(data, internalData); err != nil {
			return errors.Wrapf(err, "data is %v", string(data))
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return internalData, nil
}

func (n *NetworkStore) GetAll(ctx context.Context) (results []*networking.NCProxyNetwork, err error) {
	if err := n.DB.View(func(tx *bolt.Tx) error {
		bkt := getNetworkBucket(tx)
		if bkt == nil {
			return errors.Wrapf(ErrBucketNotFound, "network bucket %v", bucketKeyNetwork)
		}
		err := bkt.ForEach(func(k, v []byte) error {
			data := bkt.Get([]byte(k))
			if data == nil {
				return errors.Wrapf(errKeyNotFound, "network %v", k)
			}
			internalData := &networking.NCProxyNetwork{}
			if err := json.Unmarshal(data, internalData); err != nil {
				return errors.Wrapf(err, "data is %v", string(data))
			}
			results = append(results, internalData)
			return nil
		})
		return err
	}); err != nil {
		return nil, err
	}

	return results, nil
}

func (n *NetworkStore) Update(ctx context.Context, key string, network *networking.NCProxyNetwork) error {
	if err := n.DB.Update(func(tx *bolt.Tx) error {
		bkt, err := createNetworkBucket(tx)
		if err != nil {
			return err
		}
		internalData, err := json.Marshal(network)
		if err != nil {
			return err
		}
		return bkt.Put([]byte(key), internalData)
	}); err != nil {
		return err
	}
	return nil
}

func (n *NetworkStore) Delete(ctx context.Context, networkName string) error {
	if err := n.DB.Update(func(tx *bolt.Tx) error {
		bkt := getNetworkBucket(tx)
		if bkt == nil {
			return errors.Wrapf(ErrBucketNotFound, "bucket %v", bucketKeyNetwork)
		}
		return bkt.Delete([]byte(networkName))
	}); err != nil {
		return err
	}
	return nil
}

type EndpointStore struct {
	DB *bolt.DB
}

func NewEndpointStore(db *bolt.DB) *EndpointStore {
	return &EndpointStore{DB: db}
}

func (n *EndpointStore) Get(ctx context.Context, endpointName string) (*networking.NCProxyEndpoint, error) {
	endpt := &networking.NCProxyEndpoint{}
	if err := n.DB.View(func(tx *bolt.Tx) error {
		bkt := getEndpointBucket(tx)
		if bkt == nil {
			return errors.Wrapf(ErrBucketNotFound, "endpoint bucket %v", bucketKeyEndpoint)
		}
		jsonData := bkt.Get([]byte(endpointName))
		if jsonData == nil {
			return errors.Wrapf(errKeyNotFound, "endpoint %v", endpointName)
		}
		if err := json.Unmarshal(jsonData, endpt); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return endpt, nil
}

func (n *EndpointStore) GetAll(ctx context.Context) (results []*networking.NCProxyEndpoint, err error) {
	if err := n.DB.View(func(tx *bolt.Tx) error {
		bkt := getEndpointBucket(tx)
		if bkt == nil {
			return errors.Wrapf(ErrBucketNotFound, "endpoint bucket %v", bucketKeyEndpoint)
		}
		err := bkt.ForEach(func(k, v []byte) error {
			jsonData := bkt.Get([]byte(k))
			if jsonData == nil {
				return errors.Wrapf(errKeyNotFound, "endpoint %v", k)
			}
			endptInternal := &networking.NCProxyEndpoint{}
			if err := json.Unmarshal(jsonData, endptInternal); err != nil {
				return err
			}
			results = append(results, endptInternal)
			return nil
		})
		return err
	}); err != nil {
		return nil, err
	}

	return results, nil
}

func (n *EndpointStore) Update(ctx context.Context, endpointName string, endpt *networking.NCProxyEndpoint) error {
	if err := n.DB.Update(func(tx *bolt.Tx) error {
		bkt, err := createEndpointBucket(tx)
		if err != nil {
			return err
		}
		jsonEndptData, err := json.Marshal(endpt)
		if err != nil {
			return err
		}
		return bkt.Put([]byte(endpointName), jsonEndptData)
	}); err != nil {
		return err
	}
	return nil
}

func (n *EndpointStore) Delete(ctx context.Context, endpointName string) error {
	if err := n.DB.Update(func(tx *bolt.Tx) error {
		bkt := getEndpointBucket(tx)
		if bkt == nil {
			return errors.Wrapf(ErrBucketNotFound, "bucket %v", bucketKeyEndpoint)
		}
		return bkt.Delete([]byte(endpointName))
	}); err != nil {
		return err
	}
	return nil
}

// ComputeAgentStore is a database that stores a key value pair of container id
// to compute agent server address
type ComputeAgentStore struct {
	db *bolt.DB
}

func NewComputeAgentStore(db *bolt.DB) *ComputeAgentStore {
	return &ComputeAgentStore{db: db}
}

func (c *ComputeAgentStore) Close() error {
	return c.db.Close()
}

// GetComputeAgent returns the compute agent address of a single entry in the database for key `containerID`
// or returns an error if the key does not exist
func (c *ComputeAgentStore) GetComputeAgent(ctx context.Context, containerID string) (result string, err error) {
	if err := c.db.View(func(tx *bolt.Tx) error {
		bkt := getComputeAgentBucket(tx)
		if bkt == nil {
			return errors.Wrapf(ErrBucketNotFound, "bucket %v", bucketKeyComputeAgent)
		}
		data := bkt.Get([]byte(containerID))
		if data == nil {
			return errors.Wrapf(errKeyNotFound, "key %v", containerID)
		}
		result = string(data)
		return nil
	}); err != nil {
		return "", err
	}

	return result, nil
}

// GetComputeAgents returns a map of the key value pairs stored in the database
// where the keys are the containerIDs and the values are the corresponding compute agent
// server addresses
func (c *ComputeAgentStore) GetComputeAgents(ctx context.Context) (map[string]string, error) {
	content := map[string]string{}
	if err := c.db.View(func(tx *bolt.Tx) error {
		bkt := getComputeAgentBucket(tx)
		if bkt == nil {
			return errors.Wrapf(ErrBucketNotFound, "bucket %v", bucketKeyComputeAgent)
		}
		err := bkt.ForEach(func(k, v []byte) error {
			data := bkt.Get([]byte(k))
			content[string(k)] = string(data)
			return nil
		})
		return err
	}); err != nil {
		return nil, err
	}
	return content, nil
}

// UpdateComputeAgent updates or adds an entry (if none already exists) to the database
// `address` corresponds to the address of the compute agent server for the `containerID`
func (c *ComputeAgentStore) UpdateComputeAgent(ctx context.Context, containerID string, address string) error {
	if err := c.db.Update(func(tx *bolt.Tx) error {
		bkt, err := createComputeAgentBucket(tx)
		if err != nil {
			return err
		}
		return bkt.Put([]byte(containerID), []byte(address))
	}); err != nil {
		return err
	}
	return nil
}

// DeleteComputeAgent deletes an entry in the database or returns an error if none exists
// `containerID` corresponds to the target key that the entry should be deleted for
func (c *ComputeAgentStore) DeleteComputeAgent(ctx context.Context, containerID string) error {
	if err := c.db.Update(func(tx *bolt.Tx) error {
		bkt := getComputeAgentBucket(tx)
		if bkt == nil {
			return errors.Wrapf(ErrBucketNotFound, "bucket %v", bucketKeyComputeAgent)
		}
		return bkt.Delete([]byte(containerID))
	}); err != nil {
		return err
	}
	return nil
}
