FROM golang:1.26.5-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/testserver .

FROM alpine:3.23

RUN apk add --no-cache ca-certificates
COPY --from=build /out/testserver /usr/local/bin/testserver
EXPOSE 8080 6060
ENTRYPOINT ["/usr/local/bin/testserver"]
