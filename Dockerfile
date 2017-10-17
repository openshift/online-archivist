FROM golang:1.8

ENV PATH=/go/bin:$PATH GOPATH=/go

ADD . /go/src/github.com/openshift/online-archivist

WORKDIR /go/src/github.com/openshift/online-archivist
RUN make build
ENTRYPOINT ["archivist"]
