FROM golang:1.20-alpine as build
WORKDIR /app
COPY . .
RUN go build -o mock-csam main.go

FROM alpine:3.18
WORKDIR /app
COPY --from=build /app/mock-csam ./mock-csam
EXPOSE 8080
ENTRYPOINT ["./mock-csam"]
