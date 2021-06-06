FROM golang:1.13 AS build
WORKDIR $GOPATH/src/github.com/chrispyduck/kube-plex
COPY . .
RUN go get -d -v ./...
RUN go build -a -o /dist/kube-plex .

FROM alpine:latest AS dist
COPY --from=build /dist/kube-plex /kube-plex
