FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/uade-bot ./cmd/uade-bot && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/healthcheck ./cmd/healthcheck
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/uade-bot /app/uade-bot
COPY --from=build /out/healthcheck /app/healthcheck
EXPOSE 8080
ENTRYPOINT ["/app/uade-bot"]
