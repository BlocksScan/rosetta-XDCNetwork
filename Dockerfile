# Compile golang
FROM ubuntu:18.04 as golang-builder

RUN mkdir -p /app \
  && chown -R nobody:nogroup /app
WORKDIR /app

RUN apt-get update && apt-get install -y curl make gcc g++ git
ENV GOLANG_VERSION 1.15.5
ENV GOLANG_DOWNLOAD_SHA256 9a58494e8da722c3aef248c9227b0e9c528c7318309827780f16220998180a0d
ENV GOLANG_DOWNLOAD_URL https://golang.org/dl/go$GOLANG_VERSION.linux-amd64.tar.gz

RUN curl -fsSL "$GOLANG_DOWNLOAD_URL" -o golang.tar.gz \
  && echo "$GOLANG_DOWNLOAD_SHA256  golang.tar.gz" | sha256sum -c - \
  && tar -C /usr/local -xzf golang.tar.gz \
  && rm golang.tar.gz

ENV GOPATH /go
ENV PATH $GOPATH/bin:/usr/local/go/bin:$PATH
RUN mkdir -p "$GOPATH/src" "$GOPATH/bin" && chmod -R 777 "$GOPATH"
ENV GO111MODULE=on

# Compile XDPoSChain Client software
FROM golang-builder as XDC-builder

ARG XDPoSChain_CORE_VERSION="dev-upgrade"
RUN rm -rf XDC-source
RUN git clone --branch $XDPoSChain_CORE_VERSION https://github.com/xinfinorg/XDPoSChain.git XDC-source
RUN cd XDC-source && \
make clean && make XDC && chmod +x ./build/bin/XDC && \
mv ./build/bin/XDC /app/XDC && \
cp ./genesis/mainnet.json /app/genesis.json && \
cd .. && rm -rf XDC-source


# Compile XDC-rosetta
FROM golang-builder as rosetta-builder

# Use native remote build context to build in any directory
ARG XDPoSChain_ROSETTA_GATEWAY_VERSION="master"
RUN mkdir /app/XDPoSChain
RUN cd /app
RUN rm -rf XDC-rosetta-gateway-source
RUN git clone --branch $XDPoSChain_ROSETTA_GATEWAY_VERSION https://github.com/BlocksScan/rosetta-XDCNetwork.git XDC-rosetta-gateway-source
RUN cd XDC-rosetta-gateway-source && \
go build -o XDC-rosetta . && \
mv ./XDC-rosetta /app/XDC-rosetta && \
mv ./XDPoSChain/call_tracer.js /app/XDPoSChain/call_tracer.js && \
mv ./XDPoSChain/XDPoSChain.toml /app/XDPoSChain/XDPoSChain.toml && \
cd .. && rm -rf XDC-rosetta-gateway-source



## Build Final Image
FROM ubuntu:18.04

RUN mkdir -p /app \
  && chown -R nobody:nogroup /app \
  && mkdir -p /data \
  && chown -R nobody:nogroup /data

WORKDIR /app

# Copy binary from XDC-builder
COPY --from=XDC-builder /app/XDC /app/XDC
# Copy genesis from XDC-builder
COPY --from=XDC-builder /app/genesis.json /app/genesis.json

# Copy binary from rosetta-builder
COPY --from=rosetta-builder /app/XDPoSChain /app/XDPoSChain
COPY --from=rosetta-builder /app/XDC-rosetta /app/XDC-rosetta

# Set permissions for everything added to /app
RUN chmod -R 755 /app/*

CMD ["/app/XDC-rosetta", "run"]

