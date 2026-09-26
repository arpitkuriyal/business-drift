FROM golang:1.25.13-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -o /out/api ./cmd/api

FROM alpine:3.22
RUN addgroup -S backend && adduser -S -G backend app
COPY --from=build /out/api /server/api
USER app
EXPOSE 8080
ENTRYPOINT ["/server/api"]
