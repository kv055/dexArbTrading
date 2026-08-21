FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go .
RUN CGO_ENABLED=0 go build -o /proxy .

FROM alpine:3.21
COPY --from=build /proxy /proxy
USER 65532:65532
ENTRYPOINT ["/proxy"]
