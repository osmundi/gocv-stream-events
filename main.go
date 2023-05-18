// What it does:
//
// This program uses a deep neural network to perform object detection.
// Detected objects will be saved to a database and subscribed users
// will be notified with an email.
//
// If environment variable RUN_ENV is not 'prod' then the detected objects
// are not saved to a database but shown in a window for testing purposes.
//
//
// How to run:
//
// 		go run ./cmd/dnn-detection/main.go [video/stream source] [modelfile] [configfile] ([backend] [confidence])
//
// Replace video source with '--' and the source(s) will be read from database
//
// It's possible to set multiple sources seperated with comma. The streams will
// be processed in seperate go routines.
//
// Supported  sources:
//   - images (*.png, *.jpg)
//   - webcam (0)
//   - video (*.mp4)
//   - rtsp stream
//

package main

import (
	"bufio"
	"database/sql"
	"flag"
	"fmt"
	"image"
	"image/color"
	"log"
	"math"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	"gocv.io/x/gocv"
)

var db Database

var blue = color.RGBA{0, 0, 255, 0}
var yellow = color.RGBA{0, 255, 0, 0}

var confidenceTreshold float32

// this value controls overlapping bounding boxes
// default value 0.7 seems to recognize two overlapping objects
// but dont draw duplicate bounding box from the same object
var intersectionTreshold = 0.7

var model string
var config string
var backend gocv.NetBackendType
var target = gocv.NetTargetCPU

var classes = readClasses()

var numberTranslator = map[int]string{1: "One", 2: "Two", 3: "Three", 4: "Four", 5: "Five"}

//go:generate go run golang.org/x/tools/cmd/stringer -type=deviceSource
type deviceSource int

const (
	IMAGE deviceSource = iota
	VIDEO
	STREAM
)

var sourceType deviceSource

type detectedObject struct {
	confidence               float32
	top, left, width, height int
	label                    string
}

func logConfigurations(configs map[string]string) {
	for k, v := range configs {
		log.Println(k, "-", v)
	}
}

func readClasses() []string {
	var classes []string
	file, err := os.Open("./coco.names")
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		classes = append(classes, scanner.Text())
	}

	return classes
}

func sendMail(receiver string, title string, body string) {
	from := os.Getenv("EMAIL_ADDR")
	to := []string{receiver}
	smtpHost := os.Getenv("SMTP_HOST")
	message := []byte("Subject: " + title + "\r\n\r\n" + body + "\r\n")
	err := smtp.SendMail(smtpHost+":25", nil, from, to, message)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Email notification of detected object has been sent to: %s", receiver)
}

type Database struct {
	pool *sql.DB
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

func main() {
	// get environment variables
	err := godotenv.Load(".env")
	if err != nil {
		log.Fatalf("Error loading environment variables file")
	}

	// setup logging
	logfile, err := os.Create(os.Getenv("LOG_FILE"))
	if err != nil {
		log.Fatal(err)
	}
	defer logfile.Close()
	log.SetOutput(logfile)

	// init database connection
	psqlconn := fmt.Sprintf("host=%s port=%d user=%s "+
		"password=%s dbname=%s sslmode=disable",
		os.Getenv("DB_HOST"), 5432, os.Getenv("DB_USER"), os.Getenv("DB_PASSWORD"), os.Getenv("DB_NAME"))
	pool, _ := sql.Open("postgres", psqlconn)
	pingErr := pool.Ping()
	if pingErr != nil {
		log.Fatalf("Cannot connect to database %v", pingErr)
	}
	defer pool.Close()

	db = Database{pool}

	// read command line arguments
	flag.StringVar(&model, "m", "yolo-obj_final.weights", "Object detection model")
	flag.StringVar(&config, "c", "yolo-obj.cfg", "Object detection model configurations")
	confidence := flag.Int("confidence", 75, "How certain the model must be of detected objects in order to notice them")
	selectedBackend := flag.String("backend", "opencv", "Detection nets backend (opencv/openvino)")
	targetString := flag.String("target", "cpu", "Will the model be run on CPU or GPU (check gocv.ParseNetTarget for possible targets")
	deviceIds := flag.String("d", "--", "List of devices seperated by comma")

	flag.Parse()

	if *confidence <= 100 && *confidence > 0 {
		confidenceTreshold = float32(*confidence) / 100
	} else {
		fmt.Println("Confidence set to default (0.3) because provided input is too big or too low (use something between 0..1)")
		confidenceTreshold = 0.3
	}

    // serialize command line arguments
	backend = gocv.ParseNetBackend(*selectedBackend)
	if backend == gocv.NetBackendOpenVINO {
		// vpu available on 13th gen intel cpus
		target = gocv.NetTargetVPU
		target = gocv.NetTargetCPU
	}

	target = gocv.ParseNetTarget(*targetString)

	var deviceIdList []string
	if *deviceIds == "--" {
		deviceIdList = db.getStreamAddress()

	} else {
		deviceIdList = strings.Split(*deviceIds, ",")
	}

	logConfigurations(map[string]string{"devices": *deviceIds, "model": model, "config": config, "backend": *selectedBackend, "confidence": strconv.Itoa(*confidence)})

	// its possible to read from multiple streams with this same program
	var wg = &sync.WaitGroup{}
	for i, deviceID := range deviceIdList {
		wg.Add(1)
		go detectFromCapture(deviceID, i, wg)
	}
	wg.Wait()
}

func detectFromCapture(deviceID string, captureId int, wg *sync.WaitGroup) {

	var webcam *gocv.VideoCapture
	var captureError error

	var window *gocv.Window
	if os.Getenv("RUN_ENV") != "prod" {
		window = gocv.NewWindow(fmt.Sprintf("DNN Detection - %d", captureId))
		defer window.Close()
	}

	img := gocv.NewMat()
	defer img.Close()

	if strings.HasSuffix(deviceID, ".jpg") || strings.HasSuffix(deviceID, ".png") {
		img = gocv.IMRead(deviceID, gocv.IMReadColor)
		if img.Empty() {
			fmt.Printf("Error reading image from: %v\n", deviceID)
			return
		}
		sourceType = IMAGE
	} else if strings.HasSuffix(deviceID, ".mp4") || deviceID == "0" {
		// read from local video or webcam
		webcam, captureError = gocv.OpenVideoCapture(deviceID)
		if captureError != nil {
			fmt.Printf("Error opening video capture device: %v\n", deviceID)
			return
		}
		defer webcam.Close()
		sourceType = VIDEO
	} else if strings.HasPrefix(deviceID, "rtsp") {
		// open capture device (with ffmpeg)
		webcam, captureError = gocv.OpenVideoCaptureWithAPI(deviceID, 1900)
		if captureError != nil {
			fmt.Printf("Error opening video stream device: %v\n", deviceID)
			return
		}
		defer webcam.Close()
		sourceType = STREAM
	}

	// open DNN object tracking model
	net := gocv.ReadNet(model, config)

	if net.Empty() {
		fmt.Printf("Error reading network model from : %v %v\n", model, config)
		return
	}
	defer net.Close()
	net.SetPreferableBackend(gocv.NetBackendType(backend))
	net.SetPreferableTarget(gocv.NetTargetType(target))

	ratio := 1.0 / 255.0
	mean := gocv.NewScalar(0, 0, 0, 0)

	log.Printf("Start reading device (%v): %v\n", sourceType, deviceID)

	for {
		if sourceType == STREAM || sourceType == VIDEO {
			if sourceType == STREAM {
				// set 0-based index of the frame to be decoded/captured next.
				// -> this will capture the most recent image
                // Test waiting: ttime.Sleep(8 * time.Second)
				webcam.Set(1, 0)
			} else if sourceType == VIDEO {
				webcam.Grab(25)
			}
			if ok := webcam.Read(&img); !ok {
				log.Printf("Device closed: %v\n", deviceID)
				wg.Done()
				return
			}

			if img.Empty() {
				log.Fatal("cannot read image from video/stream")
				continue
			}
		}

		// try to get capture time as real as possible (this why called straight after webcam read)
		// TODO: read location from database (if you want to record from offshore cameras also)
		loc, _ := time.LoadLocation("Europe/Helsinki")
		captureTime := time.Now().In(loc).Format(time.RFC3339)

		// convert image Mat to 300x300 blob that the object detector can analyze
		blob := gocv.BlobFromImage(img, ratio, image.Pt(416, 416), mean, true, false)

		// feed the blob into the detector
		net.SetInput(blob, "")

		// run a forward pass thru the network
		ln := net.GetLayerNames()
		var fl []string
		for _, l := range net.GetUnconnectedOutLayers() {
			fl = append(fl, ln[l-1])
		}
		prob := net.ForwardLayers(fl)

		detectedObjects := performDetection(&img, prob)

		// TODO: run headless in production (no need to draw rectangles to image and show it in window)
		if os.Getenv("RUN_ENV") == "prod" {
			if len(detectedObjects) == 0 {
				continue
			}
			// all the labels are currently same (TODO: this must be updated if the model contains multiple classes)
			label := strings.Split(detectedObjects[0].label, " ")
			classId, err := db.getClassId(label[0])
			if err != nil {
				log.Fatal(err)
			}
			event, err := db.insertDetections(detectedObjects, classId, captureTime)
			if err != nil {
				log.Fatal(err)
			}
			if event > 0 {
				db.notifyObservers(deviceID, event)
			}
		} else {
			// show bounding box in own window when in test environment
			for _, obj := range detectedObjects {
				gocv.Rectangle(&img, image.Rect(obj.left, obj.top, obj.left+obj.width, obj.top+obj.height), yellow, 2)
				gocv.PutText(&img, obj.label, image.Pt(obj.left, obj.top), gocv.FontHersheyPlain, 2.2, blue, 2)
			}
			window.ResizeWindow(1200, 720)
			window.IMShow(img)
			if window.WaitKey(1) >= 0 {
				wg.Done()
				break
			}
		}
		for i := 0; i < len(prob); i++ {
			// nolint: errcheck
			defer prob[i].Close()
		}
		blob.Close()
	}
}

func bbIntersectionOverUnion(a, b detectedObject) float64 {

	boxA := []int{a.left, a.top, a.left + a.width, a.top + a.height}
	boxB := []int{b.left, b.top, b.left + b.width, b.top + b.height}
	// determine the (x, y)-coordinates of the intersection rectangle
	xA := math.Max(float64(boxA[0]), float64(boxB[0]))
	yA := math.Max(float64(boxA[1]), float64(boxB[1]))
	xB := math.Min(float64(boxA[2]), float64(boxB[2]))
	yB := math.Min(float64(boxA[3]), float64(boxB[3]))

	// compute the area of intersection rectangle
	interArea := math.Max(0, xB-xA+1) * math.Max(0, yB-yA+1)

	// compute the area of both the prediction and ground-truth rectangles
	boxAArea := float64(boxA[2]-boxA[0]+1) * float64(boxA[3]-boxA[1]+1)
	boxBArea := float64(boxB[2]-boxB[0]+1) * float64(boxB[3]-boxB[1]+1)

	// compute the intersection over union by taking the intersection area and dividing it by the sum of prediction + ground-truth areas - the intersection area
	iou := interArea / (boxAArea + boxBArea - interArea)

	// return the intersection over union value
	return iou
}

// performDetection analyzes the results from the detector network,
// which produces an output blob with a shape 1x1xNx7
// where N is the number of detections, and each detection
// is a vector of float values
// [batchId, classId, confidence, left, top, right, bottom]
func performDetection(frame *gocv.Mat, results []gocv.Mat) []detectedObject {

	detectedObjects := []detectedObject{}
	var currentlyDetectedObject detectedObject

	for _, output := range results {
		data, err := output.DataPtrFloat32()
		if err != nil {
			log.Println("no data")
		}

		if output.Cols() < 0 {
			row := data[0:10]
			fmt.Println(row)
			break
		}

		for j := 0; j < output.Total(); j += output.Cols() {
			row := data[j : j+output.Cols()]
			scores := row[5:]
			classID, confidence := getClassIDAndConfidence(scores)

			if confidence > confidenceTreshold {
				centerX := int(row[0] * float32(frame.Cols()))
				centerY := int(row[1] * float32(frame.Rows()))
				width := int(row[2] * float32(frame.Cols()))
				height := int(row[3] * float32(frame.Rows()))

				currentlyDetectedObject = detectedObject{
					confidence: confidence,
					top:        centerY - height/2,
					left:       centerX - width/2,
					width:      width,
					height:     height,
					label:      fmt.Sprintf("%s - %d%%", classes[classID], int(100*confidence)),
				}

				if len(detectedObjects) == 0 {
					log.Printf("Detected class:%s with %d%% confidence", classes[classID], int(confidence*99))
					detectedObjects = append(detectedObjects, currentlyDetectedObject)
					continue
				}

				newObject := true
				for i, obj := range detectedObjects {
					intersection := bbIntersectionOverUnion(currentlyDetectedObject, obj)
					if intersection > 0.7 {
						newObject = false

						if currentlyDetectedObject.confidence > obj.confidence {
							detectedObjects[i] = currentlyDetectedObject
						}
					}
				}

				if newObject {
					detectedObjects = append(detectedObjects, currentlyDetectedObject)
				}
			}
		}
	}

	return detectedObjects
}

// getClassID retrieve class id from given row.
func getClassIDAndConfidence(x []float32) (int, float32) {
	res := 0
	max := float32(0.0)
	for i, y := range x {
		if y > max {
			max = y
			res = i
		}
	}
	return res, max
}
