FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /bin/registry ./cmd/registry \
 && CGO_ENABLED=0 go build -o /bin/lobby ./cmd/lobby \
 && CGO_ENABLED=0 go build -o /bin/world ./cmd/world

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /bin/registry /usr/bin/registry
COPY --from=build /bin/lobby /usr/bin/lobby
COPY --from=build /bin/world /usr/bin/world
