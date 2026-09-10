ARG BASE_IMAGE=golang:trixie

FROM $BASE_IMAGE

RUN chmod -R a+rwx /usr/local/go/pkg

ENV HOME=/go/src/github.com/sannrox/rusui

WORKDIR ${HOME}

# Make output from the dockerized build go someplace else
ENV OUTPUT_SUBPATH=_output/dockerized

# Make log messages use the right timezone
ADD localtime /etc/localtime
RUN chmod a+r /etc/localtime

RUN chmod -R a+rwx $GOPATH

# VERSION arg busts cache when build/build-image/VERSION changes
ARG VERSION=1
RUN apt-get update -y && apt-get upgrade -y \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        build-essential \
        git \
        make \
        shellcheck \
    && rm -rf /var/lib/apt/lists/*

RUN chmod -R a+rwx $GOPATH
