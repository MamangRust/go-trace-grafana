# Use an official Golang runtime as a parent image
FROM golang:1.23-alpine

# Install build dependencies for CGO
RUN apk add --no-cache gcc musl-dev

# Set the working directory in the container
WORKDIR /app

# Copy go.mod and go.sum files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy the source code into the container
COPY . .

# Enable CGO and build the Go application
ENV CGO_ENABLED=1
RUN go build -o main .

# Expose port 8000
EXPOSE 8000

# Command to run the Go app
CMD ["./main"]
