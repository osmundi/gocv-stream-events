# gocv-stream-events


### Dependencies

[gocv (opencv >4.7.0)](https://pkg.go.dev/gocv.io/x/gocv#readme-installation)

### Prerequisites

A [YOLO](https://pjreddie.com/darknet/yolo/) model is needed 

### Build
```
docker build -t detector:latest .
```

Create prod.env with the next environment variables (or set them directly in docker-compose.yml):
 - DB_HOST=db
 - DB_USER=seili
 - DB_PASSWORD=seilipassword
 - DB_NAME=seili_osprey_nest
 - EMAIL_ADDR=
 - SMTP_HOST=
 - RUN_ENV=prod
 - LOG_FILE=container.log


Create mounting points for database and logs:
```
touch container.log
mkdir dbdata
```

### Run

```
docker-compose up -d
```
