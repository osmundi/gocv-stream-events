# gocv-stream-events


### Run in docker:
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


```
docker-compose up -d
```
