# The panel's container image.
#
# Three stages so that a change to the Go code does not reinstall npm packages,
# and so that the image that ships contains a single static binary and nothing
# else: no shell, no package manager, no interpreter to be exploited.

FROM node:22-alpine AS frontend
WORKDIR /src/web
# Dependencies first: this layer is reused until package-lock.json changes.
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
# The build copies llms.txt in so the panel can serve it at /llms.txt, and it
# lives at the repository root rather than inside web/. Without this line the
# image build fails on a missing file — which it did, on every attempt, because
# `make image` needs a Docker daemon and nothing here ever had one.
COPY llms.txt /src/llms.txt
RUN npm run build

FROM golang:1.26-alpine AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /src/web/dist ./web/dist
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
# CGO off: the SQLite driver is pure Go, so the result runs on a distroless
# image with no libc at all.
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w \
      -X skifity/internal/version.Version=${VERSION} \
      -X skifity/internal/version.Commit=${COMMIT} \
      -X skifity/internal/version.Date=${DATE}" \
    -o /out/skifity ./cmd/skifity

# The command line tool for every other platform, for /api/cli/download.
#
# The panel runs on Linux and hands out the binary it is running, which is the
# one file that will not run on the Mac or the Windows laptop most people
# deploy from. So the image carries the rest, gzipped — five binaries are most
# of the image otherwise — and the panel unpacks one as it is downloaded.
# CLI_PLATFORMS="" leaves them out, for an image that has to be small.
ARG CLI_PLATFORMS="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64"
RUN mkdir -p /out/cli && \
    self="$(go env GOOS)/$(go env GOARCH)" && \
    for target in ${CLI_PLATFORMS}; do \
      [ "$target" = "$self" ] && continue; \
      os="${target%/*}"; arch="${target#*/}"; ext=""; \
      [ "$os" = "windows" ] && ext=".exe"; \
      CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath \
        -ldflags "-s -w \
          -X skifity/internal/version.Version=${VERSION} \
          -X skifity/internal/version.Commit=${COMMIT} \
          -X skifity/internal/version.Date=${DATE}" \
        -o "/out/cli/skifity-$os-$arch$ext" ./cmd/skifity && \
      gzip -9 "/out/cli/skifity-$os-$arch$ext" || exit 1; \
    done

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=backend /out/skifity /usr/local/bin/skifity
COPY --from=backend /out/cli /usr/local/share/skifity/cli

# 65532 is distroless's "nonroot" user. The installer chowns the panel's
# directories to it, so the container never needs to run as root.
USER 65532:65532
WORKDIR /var/lib/skifity
EXPOSE 8080

# The health endpoint answers before the cluster connection is established, so
# a panel that cannot reach Kubernetes still reports why instead of restarting.
ENTRYPOINT ["/usr/local/bin/skifity"]
CMD ["server"]
