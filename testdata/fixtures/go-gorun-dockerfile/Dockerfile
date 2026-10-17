FROM golang:1.22
WORKDIR /app
COPY . .
RUN go mod download
EXPOSE 9000
CMD ["go", "run", "./cmd/server", "-addr", ":9000"]
