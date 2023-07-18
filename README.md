# gocv-stream-events


### Dependencies

[gocv (opencv >4.7.0)](https://pkg.go.dev/gocv.io/x/gocv#readme-installation)

### Prerequisites

A [YOLO](https://pjreddie.com/darknet/yolo/) model (configuration and weights) is needed in order to run the realtime object detection. Basic yolo v4 weigts can be downloaded from [here](https://github.com/AlexeyAB/darknet/releases/download/darknet_yolo_v4_pre/yolov4.weights) and the configuration from [here](https://raw.githubusercontent.com/AlexeyAB/darknet/master/cfg/yolov4.cfg). The application will search models from the models/default -path.


### Build

#### CLI
1. Initialize postgresql database with init.sql
2. Set .env based on template.env and the database credentials you just created 
3. Build with `go build`

#### Docker
1. Build the application
```
docker build -t detector:latest .
```
2. Create docker.env based on template.env
3. Set up database credentials to the init.sql (set the same credentials to docker-compose.yml also)
4. Create mounting points for database and logs:
```
touch container.log
mkdir dbdata
```

### Run

#### Docker
Set up commandline parameters in docker-compose.yml and run:
```
docker-compose up -d
```

#### CLI
```
./gocv-stream-events -h
```




