FROM golang:1.27-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/demo-worker ./cmd/demo-worker

FROM debian:bookworm-slim
COPY --from=build /out/demo-worker /demo-worker
ENTRYPOINT ["/demo-worker"]
