# to build this docker image:
#   docker build .
FROM gocv/opencv:4.7.0 as build

WORKDIR /go/src

# Get dependencies - will also be cached if we won't change mod/sum
COPY go.mod . 
COPY go.sum .
RUN go mod download
#RUN go vet -v
#RUN go test -v

# COPY the source code as the last step
COPY . .

RUN go build -o /go/bin/app

CMD ["/go/bin/app", "-d", "testi.mp4", "-m", "yolo-obj_final.weights", "-c", "yolo-obj.cfg"]

