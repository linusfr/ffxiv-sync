FROM golang:1.26-alpine AS build
WORKDIR /src
# Nothing beyond the standard library, so there is no module cache layer worth
# having.
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /ffsync-server ./cmd/ffsync-server
# Created here so it can be copied in with an owner. A bare VOLUME /data would
# be made by the daemon at run time, owned by root, and the nonroot user could
# not write into it.
RUN mkdir /data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /ffsync-server /ffsync-server
COPY --from=build --chown=65532:65532 /data /data
ENV FFSYNC_ADDR=0.0.0.0:8771 \
    FFSYNC_DATA=/data
EXPOSE 8771
VOLUME /data
USER nonroot:nonroot
ENTRYPOINT ["/ffsync-server"]
