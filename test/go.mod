module github.com/Microsoft/hcsshim/test

go 1.13

require (
	github.com/Microsoft/go-winio v0.4.15-0.20200908182639-5b44b70ab3ab
	github.com/Microsoft/hcsshim v0.8.7
	github.com/blang/semver v3.1.0+incompatible // indirect
	github.com/containerd/containerd v1.3.2
	github.com/containerd/cri/criextension v0.0.0-00010101000000-000000000000
	github.com/containerd/go-runc v0.0.0-20180907222934-5a6d9f37cfa3
	github.com/containerd/ttrpc v0.0.0-20190828154514-0e0f228740de
	github.com/containerd/typeurl v0.0.0-20180627222232-a93fcdb778cd
	github.com/docker/distribution v2.7.1-0.20190205005809-0d3efadf0154+incompatible // indirect
	github.com/docker/go-events v0.0.0-20190806004212-e31b211e4f1c // indirect
	github.com/gogo/googleapis v1.4.0 // indirect
	github.com/gogo/protobuf v1.3.1
	github.com/hashicorp/errwrap v0.0.0-20141028054710-7554cd9344ce // indirect
	github.com/hashicorp/go-multierror v0.0.0-20161216184304-ed905158d874 // indirect
	github.com/imdario/mergo v0.3.8 // indirect
	github.com/opencontainers/image-spec v1.0.1 // indirect
	github.com/opencontainers/runtime-spec v1.0.2
	github.com/opencontainers/runtime-tools v0.0.0-20181011054405-1d69bd0f9c39
	github.com/pkg/errors v0.9.1
	github.com/sirupsen/logrus v1.4.2
	github.com/syndtr/gocapability v0.0.0-20200815063812-42c35b437635 // indirect
	github.com/xeipuuv/gojsonpointer v0.0.0-20180127040702-4e3ac2762d5f // indirect
	github.com/xeipuuv/gojsonreference v0.0.0-20180127040603-bd5ef7bd5415 // indirect
	github.com/xeipuuv/gojsonschema v0.0.0-20180618132009-1d523034197f // indirect
	go.etcd.io/bbolt v1.3.3 // indirect
	golang.org/x/sync v0.0.0-20190423024810-112230192c58
	google.golang.org/grpc v1.33.0
	k8s.io/cri-api v0.19.2
)

replace github.com/Microsoft/hcsshim => ../

replace github.com/containerd/cri/criextension => github.com/katiewasnothere/cri/criextension v0.0.0-20201023181724-bfcad96bd1e5
