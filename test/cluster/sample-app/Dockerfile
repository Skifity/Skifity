# The smallest thing that is still a real application.
#
# It exists so `verify.sh` can deploy from Git rather than from a prebuilt
# image: building is half of what a PaaS does, and a check that skips it proves
# only that Kubernetes can run a container somebody else made.
#
# No package manager, no base image with a shell, nothing to download at build
# time. If this fails to build, the builder is what failed.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY main.go .
RUN CGO_ENABLED=0 go build -ldflags '-s -w' -o /app main.go

FROM gcr.io/distroless/static-debian12
COPY --from=build /app /app
# 1000 is the user Skifity's pod security context expects.
USER 1000
EXPOSE 8080
ENTRYPOINT ["/app"]
