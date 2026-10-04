# Stage 1: the SPA.
FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Stage 2: the static binaries, the SPA embedded in hm.
FROM golang:1.26-alpine AS build
# VERSION defaults to the VERSION file; COMMIT comes from the host, as .git
# is not in the build context.
ARG VERSION=
ARG COMMIT=none
ENV CGO_ENABLED=0
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN V="${VERSION:-$(cat VERSION)}" && go build -trimpath \
    -ldflags "-s -w -X github.com/Sznapsollo/health-monitor-nj/internal/buildinfo.Version=${V} -X github.com/Sznapsollo/health-monitor-nj/internal/buildinfo.Commit=${COMMIT}" \
    -o /out/hm ./cmd/hm
RUN go build -trimpath -ldflags "-s -w" -o /out/soak ./cmd/soak
RUN mkdir -p /out/data

# The soak traffic generator: `docker compose --profile soak up`.
FROM gcr.io/distroless/static-debian12:nonroot AS soak
COPY --from=build /out/soak /soak
COPY soak*.yaml /etc/soak/
USER nonroot:nonroot
ENTRYPOINT ["/soak"]
CMD ["-config", "/etc/soak/soak.yaml"]

# The monitor; the default target. The shipped platform files are copied
# into /data/platforms on start wherever the volume lacks them.
FROM gcr.io/distroless/static-debian12:nonroot AS hm
COPY --from=build /out/hm /hm
COPY --chown=nonroot:nonroot config.example.yaml /etc/hm/config.yaml
COPY platforms/ /usr/share/hm/platforms/
# A named volume takes the ownership of the directory it is mounted over.
COPY --from=build --chown=nonroot:nonroot /out/data /data
ENV HM_SEED_PLATFORMS_FROM=/usr/share/hm/platforms
VOLUME ["/data"]
EXPOSE 8081/tcp 8082/udp
USER nonroot:nonroot
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/hm", "-config", "/etc/hm/config.yaml", "-healthcheck"]
ENTRYPOINT ["/hm"]
CMD ["-config", "/etc/hm/config.yaml"]
