VERSION ?= 0.1.0

.PHONY: build test check release
build:
	go build -buildvcs=false -trimpath -ldflags '-s -w -X main.version=$(VERSION)' -o bin/gameap-fastdl ./cmd/gameap-fastdl
test:
	go test -race ./...
check:
	go vet ./...
release:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -trimpath -ldflags '-s -w -X main.version=$(VERSION)' -o dist/gameap-fastdl-linux-amd64 ./cmd/gameap-fastdl
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -buildvcs=false -trimpath -ldflags '-s -w -X main.version=$(VERSION)' -o dist/gameap-fastdl-linux-arm64 ./cmd/gameap-fastdl
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -buildvcs=false -trimpath -ldflags '-s -w -X main.version=$(VERSION)' -o dist/gameap-fastdl-windows-amd64.exe ./cmd/gameap-fastdl
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -buildvcs=false -trimpath -ldflags '-s -w -X main.version=$(VERSION)' -o dist/gameap-fastdl-windows-arm64.exe ./cmd/gameap-fastdl
