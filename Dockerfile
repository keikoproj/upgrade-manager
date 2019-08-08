# Build the manager binary
FROM golang:1.12.5 as builder

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY main.go main.go
COPY api/ api/
COPY controllers/ controllers/

# Build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GO111MODULE=on go build -a -o manager main.go

# Add kubectl
RUN curl -L https://storage.googleapis.com/kubernetes-release/release/v1.14.4/bin/linux/amd64/kubectl -o /usr/local/bin/kubectl
RUN chmod +x /usr/local/bin/kubectl

# Add busybox
FROM busybox:1.31 as shelladder

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:latest
WORKDIR /

COPY --from=shelladder /bin/sh /bin/sh
COPY --from=builder /workspace/manager .
COPY --from=builder /usr/local/bin/kubectl /usr/local/bin/kubectl
ENTRYPOINT ["/manager"]
