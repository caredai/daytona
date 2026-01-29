FROM rust:alpine3.23 AS mount-s3

RUN apk add --no-cache fuse fuse-dev make cmake g++ clang-dev git

RUN git clone --single-branch --branch v1.22.0 --recurse-submodules https://github.com/awslabs/mountpoint-s3

WORKDIR /mountpoint-s3

ENV RUSTFLAGS="-C target-feature=-crt-static"

RUN cargo build --release

FROM node:22-alpine AS build

RUN npm install -g corepack && corepack enable

COPY --from=golang:1.23.5-alpine /usr/local/go/ /usr/local/go/

ENV PATH="/usr/local/go/bin:${PATH}"

WORKDIR /daytona

COPY . .

ARG VERSION=0.0.1
ENV VERSION=${VERSION}

RUN yarn

RUN SKIP_COMPUTER_USE_BUILD=true yarn nx build runner --configuration=production --nxBail=true

FROM docker:28.2.2-dind-alpine3.22 AS runner

RUN apk add --no-cache curl rsync fuse-dev g++

WORKDIR /usr/local/bin

COPY --from=mount-s3 /mountpoint-s3/target/release/mount-s3 mount-s3
COPY --from=build /daytona/dist/apps/runner daytona-runner

RUN chmod +x daytona-runner

RUN mkdir -p /etc/docker && echo '{"insecure-registries": ["registry:6000"]}' > /etc/docker/daemon.json

HEALTHCHECK CMD [ "curl", "-f", "http://localhost:3003/" ]

ENTRYPOINT ["sh", "-c", "/usr/local/bin/dockerd-entrypoint.sh & daytona-runner"]
