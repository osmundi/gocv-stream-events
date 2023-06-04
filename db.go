package main

import (
	"database/sql"
	"fmt"
	"log"
	"time"
)

type Database struct {
	pool *sql.DB
}

func NewDatabaseConnection(connString string) (*Database, error) {

	pool, err := sql.Open("postgres", connString)

	if err != nil {
		return nil, err
	}

	err = pool.Ping()
	if err != nil {
		return nil, err
	}

	return &Database{pool}, nil
}

func (db Database) getClassId(label string) (int, error) {
	var class_id int
	err := db.pool.QueryRow("SELECT class_id FROM classes WHERE label=$1", label).Scan(&class_id)
	switch {
	case err == sql.ErrNoRows:
		log.Fatalf("no class with label %s\n", label)
		return 0, err
	case err != nil:
		log.Fatalf("query error: %v\n", err)
		return 0, err
	default:
		return class_id, nil
	}
}

func (db Database) insertDetections(detectedObjects []detectedObject, classId int, captureTime string) (int, error) {
	var lastInsertId int
	err := db.pool.QueryRow("INSERT INTO detection_event(class, count, created) values($1, $2, $3) RETURNING id", classId, len(detectedObjects), captureTime).Scan(&lastInsertId)
	if err != nil {
		return 0, err
	}

	for _, obj := range detectedObjects {
		_, err := db.pool.Exec("INSERT INTO detection(confidence, location_top, location_left, width, height, event) VALUES($1,$2,$3,$4,$5,$6)",
			int(obj.confidence*100), obj.top, obj.left, obj.width, obj.height, lastInsertId)
		if err != nil {
			return 0, err
		}
	}

	return lastInsertId, nil
}

func (db Database) hasBeenAlerted(email string, event int) bool {
	var alertInterval string
	var subscriptionId int
	var intervalType string
	var intervalLength int
	err := db.pool.QueryRow("SELECT id, alert_interval FROM subscription WHERE observer_id=(SELECT id from observer WHERE email=$1)", email).Scan(&subscriptionId, &alertInterval)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Sscanf(alertInterval, "%d%s", &intervalLength, &intervalType)

	loc, _ := time.LoadLocation("Europe/Helsinki")
	captureTime := time.Now().In(loc)

	var lastCapture string
	_ = db.pool.QueryRow("SELECT created FROM alert WHERE subscription_id=$1 ORDER BY created DESC", subscriptionId).Scan(&lastCapture)

	if len(lastCapture) > 0 {
		lastCaptureTime, timeParsingError := time.ParseInLocation("2006-01-02T15:04:05Z", lastCapture, loc)
		if timeParsingError != nil {
			log.Fatal(timeParsingError)
		}

		switch {
		case intervalType == "m":
			if lastCaptureTime.After(captureTime.Add(-(time.Minute * time.Duration(intervalLength)))) {
				return true
			}
		case intervalType == "h":
			if lastCaptureTime.After(captureTime.Add(-(time.Hour * time.Duration(intervalLength)))) {
				return true
			}
		case intervalType == "d":
			if lastCaptureTime.After(captureTime.AddDate(0, 0, -intervalLength)) {
				return true
			}
		default:
			return true
		}
	}

	_, err = db.pool.Exec("INSERT INTO alert (detection_event_id, subscription_id, created) VALUES ($1,$2,$3 )", event, subscriptionId, captureTime)
	if err != nil {
		log.Fatal(err)
	}
	return false

}

func (db Database) notifyObservers(deviceID string, event int) {
	rows, err := db.pool.Query("SELECT email FROM observer WHERE id IN (SELECT observer_id FROM subscription WHERE stream_id=(SELECT id FROM stream WHERE address=$1) AND alert=TRUE);", deviceID)

	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err != nil {
			log.Fatal(err)
		}

		if !db.hasBeenAlerted(email, event) {
			var classId, count int
			var stream, link string
			_ = db.pool.QueryRow("SELECT name,link FROM stream WHERE address=$1", deviceID).Scan(&stream, &link)
			err = db.pool.QueryRow("SELECT class,count FROM detection_event WHERE id=$1", event).Scan(&classId, &count)
			if err != nil {
				log.Fatal(err)
			}
			body := fmt.Sprintf("%s %s's detected at the stream of %s\n\nCheck stream at: %s\n\n***You are receiving this automatic notification because you have subscribed to the observer list of said stream***\n\nBr,\nBird detector agent", numberTranslator[count], classes[classId-1], stream, link)
			log.Println(body)
			sendMail(email, fmt.Sprintf("Detected object in: %s", stream), body)
		}
	}
}

func (db Database) getStreamAddress() []string {
	var streams []string
	var addr string
	rows, err := db.pool.Query("SELECT address FROM stream")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		if err := rows.Scan(&addr); err != nil {
			log.Fatal(err)
		}

		if addr != "" {
			streams = append(streams, addr)
		}

	}
	return streams
}
