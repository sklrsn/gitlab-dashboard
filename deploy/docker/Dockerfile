# Build from the repository root:
#   docker build -f deploy/docker/Dockerfile -t gitlab-dashboard .

FROM node:24-alpine AS ui
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN rm -rf dist/assets dist/index.html && npm run build

FROM golang:1.27-alpine AS go
ARG VERSION=dev
ARG COMMIT=unknown
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
COPY --from=ui /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
      -o /out/gitlab-dashboard ./cmd/gitlab-dashboard

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go /out/gitlab-dashboard /usr/local/bin/gitlab-dashboard
ENV LISTEN_ADDR=:8080
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/gitlab-dashboard"]
CMD ["serve"]
