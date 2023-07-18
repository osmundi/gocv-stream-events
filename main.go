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
	"flag"
	"fmt"
	"image"
	"image/color"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"context"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	"gocv.io/x/gocv"
)

var model string
var config string
var backend gocv.NetBackendType
var target = gocv.NetTargetCPU

// coco.names
var classes []string

// global database connection pool for ease of development
var db *Database

// the threshold where the recognitions will be taken into consideration
// use high enough value (e.g. over 0.95) in order to avoid false positives
var confidenceTreshold float32

// this value controls overlapping bounding boxes
// default value 0.7 seems to recognize two overlapping objects
// but dont draw duplicate bounding box from the same object
var intersectionTreshold = 0.7

var blue = color.RGBA{0, 0, 255, 0}
var yellow = color.RGBA{0, 255, 0, 0}

var logfile *os.File

func init() {
	// get environment variables
	err := godotenv.Load(".env")
	if err != nil {
		log.Fatalf("Error loading environment variables file")
	}

	// setup logging
	logfile, err = os.Create(os.Getenv("LOG_FILE"))
	if err != nil {
		log.Fatal(err)
	}
	log.SetOutput(logfile)

	// init database connection
	psqlconn := fmt.Sprintf("host=%s port=%d user=%s "+
		"password=%s dbname=%s sslmode=disable",
		os.Getenv("DB_HOST"), 5432, os.Getenv("DB_USER"), os.Getenv("DB_PASSWORD"), os.Getenv("DB_NAME"))

	db, err = NewDatabaseConnection(psqlconn)

	if err != nil {
		log.Fatal(err)
	}
}

func init() {
	// initialize detectable classes to a variable
	classes = readClasses()
}

func main() {

	defer db.pool.Close()
	defer logfile.Close()

	// read command line arguments
	flag.StringVar(&model, "m", "models/default/yolov4.weights", "Object detection model")
	flag.StringVar(&config, "c", "models/default/yolov4-custom.cfg", "Object detection model configurations")
	confidence := flag.Int("confidence", 75, "How certain the model must be of detected objects in order to notice them")
	selectedBackend := flag.String("backend", "opencv", "Detection nets backend (opencv/openvino)")
	targetString := flag.String("target", "cpu", "Will the model be run on CPU or GPU (check gocv.ParseNetTarget for possible targets")
	deviceIds := flag.String("d", "--", "List of devices seperated by comma")

	flag.Parse()

	if *confidence <= 100 && *confidence > 0 {
		confidenceTreshold = float32(*confidence) / 100
	} else {
		fmt.Println("Confidence set to default (0.75) because provided input is too big or too low (use something between 0..100)")
		confidenceTreshold = 0.75
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

	log.Println("*** run main ***")
	logConfigurations(map[string]string{"devices": *deviceIds, "model": model, "config": config, "backend": *selectedBackend, "confidence": strconv.Itoa(*confidence)})
	defer log.Println("*** end run ***")

	// its possible to read from multiple streams with this same program
	var wg = &sync.WaitGroup{}
	for i, deviceID := range deviceIdList {
		wg.Add(1)

		sourceType := getDeviceType(deviceID)
		if sourceType < 0 {
			log.Printf("Unrecognized device: %s", deviceID)
			continue
		}

		go detectFromCapture(sourceType, deviceID, i, wg)
	}
	wg.Wait()
}

func detectFromCapture(sourceType deviceSource, deviceID string, captureId int, wg *sync.WaitGroup) {

	var webcam *gocv.VideoCapture
	var captureError error
	img := gocv.NewMat()
	defer img.Close()

	if sourceType == IMAGE {
		img = gocv.IMRead(deviceID, gocv.IMReadColor)
		if img.Empty() {
			fmt.Printf("Error reading image from: %v\n", deviceID)
			return
		}
	} else if sourceType == VIDEO {
		// read from local video or webcam
		webcam, captureError = gocv.OpenVideoCapture(deviceID)
		if captureError != nil {
			fmt.Printf("Error opening video capture device: %v\n", deviceID)
			return
		}
		defer webcam.Close()
	} else if sourceType == STREAM {
		// open capture device (with ffmpeg)

		// Create timeout of 5 seconds
		ctxTimeout, cancel := context.WithTimeout(context.Background(), time.Second*5)
		defer cancel()

		c1 := make(chan *gocv.VideoCapture, 1)

		go func() {
			wc, err := gocv.OpenVideoCaptureWithAPI(deviceID, 1900)
			if err != nil {
				fmt.Printf("Error opening video stream device: %v\n", deviceID)
                wg.Done()
				return
			}
			c1 <- wc
		}()

		select {
		case webcam = <-c1:
			fmt.Printf("connection to %s succesful", deviceID)
        case <-ctxTimeout.Done():
            wg.Done()
			fmt.Printf("connetion to %s timeouted", deviceID)
            return
		}

		defer webcam.Close()
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
        // capture image from video/stream
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

		if os.Getenv("RUN_ENV") == "prod" {
            // save detections to database in production environment
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
			window := gocv.NewWindow(fmt.Sprintf("DNN Detection - %d", captureId))
			defer window.Close()
			drawBoundingBoxes(img, detectedObjects, window)
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

func drawBoundingBoxes(img gocv.Mat, detectedObjects []detectedObject, window *gocv.Window) {
	for _, obj := range detectedObjects {
		gocv.Rectangle(&img, image.Rect(obj.left, obj.top, obj.left+obj.width, obj.top+obj.height), yellow, 2)
		gocv.PutText(&img, obj.label, image.Pt(obj.left, obj.top), gocv.FontHersheyPlain, 2.2, blue, 2)
	}
	window.ResizeWindow(1200, 720)
	window.IMShow(img)
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
