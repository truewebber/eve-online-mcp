# syntax=docker/dockerfile:1

# Matches go.mod. Not used by default Compose (Postgres only);
# the binary runs on the host so OAuth callbacks stay simple.
FROM golang:1.26.5 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /eve-mcp ./cmd/eve-mcp

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /eve-mcp /eve-mcp
EXPOSE 8765 8766
USER nonroot:nonroot
ENTRYPOINT ["/eve-mcp"]
