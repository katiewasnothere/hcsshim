package api

import (
	// go mod will not vendor without an import for v1alpha2/api.proto
	_ "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
)
