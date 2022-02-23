test: fmt vet
	go test ./... -coverprofile coverage.txt
	go tool cover -html=./coverage.txt -o cover.html


# Run go fmt against code
fmt:
	go fmt ./...

# Run go vet against code
vet:
	go vet ./...