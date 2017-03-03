FROM golang:wheezy

RUN go get github.com/typhoonzero/GPUMon
ENTRYPOINT ["GPUMon"]
