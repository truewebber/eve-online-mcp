FROM golang:1.26.7 AS build

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
