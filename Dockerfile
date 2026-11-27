FROM golang:1.25-bookworm@sha256:3b4a11519ad929d1e1d261a12cff056f0c85b735253d7d861346b9c6f8b36437 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY VERSION ./
ARG REVISION=dev
RUN CGO_ENABLED=0 go build -mod=readonly -trimpath \
    -ldflags="-s -w -X main.version=$(cat VERSION) -X main.revision=${REVISION}" \
    -o /out/gateway ./cmd/gateway

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/gateway /gateway
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/gateway"]
