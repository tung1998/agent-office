# office server: static Go binary (modernc sqlite, no CGO).
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN go vet ./... && go test ./... \
 && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/office ./cmd/office

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -u 10001 office \
 && mkdir -p /data && chown office /data
COPY --from=build /out/office /usr/local/bin/office
USER office
WORKDIR /data
EXPOSE 8787
ENTRYPOINT ["office"]
CMD ["run", "--api", "0.0.0.0:8787"]
