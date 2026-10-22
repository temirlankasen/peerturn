FROM golang:1.27.3@sha256:f24164268fc1e741bbdc0066dbadcf1c8426c5a8489f0729c02c3905a53032fd AS build
WORKDIR /src
RUN test "$(go env GOVERSION)" = "go1.27.3"
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY packaging/docker/licenses.sh /licenses.sh
RUN sh /licenses.sh
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /peerturn ./cmd/peerturn \
    && mkdir -p /state && chmod 0700 /state && chown 65532:65532 /state

FROM scratch
COPY LICENSE NOTICE /licenses/
COPY --from=build /licenses/ /licenses/
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /peerturn /peerturn
COPY --from=build --chown=65532:65532 /state /var/lib/peerturn
USER 65532:65532
EXPOSE 8443
VOLUME ["/var/lib/peerturn"]
ENTRYPOINT ["/peerturn", "relay", "--config", "/etc/peerturn/relay.toml"]
