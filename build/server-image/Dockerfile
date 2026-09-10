ARG BASE_IMAGE=debian:trixie-slim

FROM "${BASE_IMAGE}"

ARG BINARY

RUN apt-get update -y && apt-get upgrade -y \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY ${BINARY} /usr/local/bin/app
RUN chmod +x /usr/local/bin/app

ENTRYPOINT ["/usr/local/bin/app"]
