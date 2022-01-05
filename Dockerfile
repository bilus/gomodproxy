FROM golang:1.16.7 as build
ENV HOME /opt/app
COPY . $HOME
WORKDIR $HOME
RUN ls $HOME
RUN go build cmd/gomodproxy/main.go && \
    go clean

FROM debian:buster
COPY --from=build /go/bin/ /go/bin/
ENTRYPOINT ["/go/bin/gomodproxy"]
