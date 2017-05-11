# Free Tier Force Sleep controller

#FROM rhel7.2:7.2-released
FROM golang:1.7

ENV PATH=/go/bin:$PATH GOPATH=/go

#LABEL com.redhat.component="oso-archivist" \
      #name="openshift3/oso-archivist" \
      #version="v1.0.0" \
      #architecture="x86_64"

ADD . /go/src/github.com/openshift/online/archivist

#RUN yum-config-manager --enable rhel-7-server-optional-rpms && \
    #INSTALL_PKGS="golang make" && \
    #yum install -y --setopt=tsflags=nodocs $INSTALL_PKGS && \
    #rpm -V $INSTALL_PKGS && \
    #yum clean all -y

WORKDIR /go/src/github.com/openshift/online/archivist
RUN make build TARGET=prod
ENTRYPOINT ["archivist"]
