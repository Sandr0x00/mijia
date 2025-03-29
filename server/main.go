package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"sort"
	"time"

	_ "github.com/mattn/go-sqlite3" // SQLite driver
)

type SensorData struct {
	Temp         float64
	Humidity     float64
	BatteryMV    int16
	BatteryLevel int8
	Timestamp    string
	DewPoint     float64
	AbsHum       float64
	BatteryIcon  string
	DewPointText string
	Mac          string
	Loc          string
	TimeRelative string
}

type Config struct {
	Loc string `json:"loc"`
	Db  *sql.DB
}

type ConfigMap map[string]Config

var configMap ConfigMap

func renderHomePage(w http.ResponseWriter, r *http.Request) {
	tmpl := template.Must(template.ParseFiles("templates/index.html"))
	if err := tmpl.Execute(w, nil); err != nil {
		http.Error(w, "Error rendering template", http.StatusInternalServerError)
	}
}

func calcDewPoint(hum float64, temp float64) float64 {
	const1 := 241.2
	const2 := 17.5043
	if temp < 0 {
		const1 = 272.186
		const2 = 22.4433
	}

	hum /= 100.0
	hum = math.Log(hum)
	abs_temp := temp / (const1 + temp)
	point := (const1*hum + (const1*const2)*abs_temp) / (const2 - hum - const2*abs_temp)
	point = math.Round(point*10) / 10
	return point
}

func calcAbsHum(hum float64, temp float64) float64 {
	abs := 13.2471 * math.Exp(17.67*temp/(temp+243.5)) * hum / (273.15 + temp)
	abs = math.Round(abs*10) / 1
	return abs
}

func plural(number int64, part string) string {
	if number == 1 {
		return ""
	} else {
		return part
	}
}

func loadSensorData(w http.ResponseWriter, r *http.Request) {
	var err error

	var data []SensorData
	for mac, config := range configMap {
		// get latest data for device
		var sensor SensorData
		sensor.Mac = mac
		sensor.Loc = config.Loc
		err = config.Db.QueryRow(`
			SELECT temp, humidity, battery_mv, battery_level, timestamp
			FROM sensor_data
			ORDER BY timestamp DESC
			LIMIT 1
		`).Scan(
			&sensor.Temp,
			&sensor.Humidity,
			&sensor.BatteryMV,
			&sensor.BatteryLevel,
			&sensor.Timestamp,
		)
		if err != nil {
			http.Error(w, "Data could not be loaded", http.StatusInternalServerError)
		}
		sensor.Humidity /= 100
		sensor.Temp /= 100

		sensor.BatteryIcon = "fa-battery-exclamation"
		if sensor.BatteryLevel < 5 {
			sensor.BatteryIcon = "fa-battery-empty red"
		} else if sensor.BatteryLevel < 15 {
			// TODO: fa-battery-low does not work atm
			sensor.BatteryIcon = "fa-battery-empty yellow"
		} else if sensor.BatteryLevel < 35 {
			sensor.BatteryIcon = "fa-battery-quarter"
		} else if sensor.BatteryLevel < 65 {
			sensor.BatteryIcon = "fa-battery-half"
		} else if sensor.BatteryLevel < 85 {
			sensor.BatteryIcon = "fa-battery-three-quarters"
		} else {
			sensor.BatteryIcon = "fa-battery-full green"
		}

		sensor.DewPoint = calcDewPoint(sensor.Humidity, sensor.Temp)
		sensor.AbsHum = calcAbsHum(sensor.Humidity, sensor.Temp)
		if sensor.Temp < 0 {
			sensor.DewPointText = "Freezing point"
		} else {
			sensor.DewPointText = "Dew point"
		}

		// time stuff
		timestamp, err := time.Parse(time.RFC3339, sensor.Timestamp)
		if err == nil {
			diff_seconds := time.Now().Unix() - timestamp.Unix()
			diff_minutes := diff_seconds / 60
			diff_hours := diff_minutes / 60
			diff_days := diff_hours / 24
			if diff_seconds < 60 {
				// less than 1 minute ago
				sensor.TimeRelative = "Up to date"
			} else if diff_minutes < 60 {
				// less than 1 hour ago
				sensor.TimeRelative = fmt.Sprintf("%d Minute%s ago", diff_minutes, plural(diff_minutes, "s"))
			} else if diff_hours < 24 {
				// less than 1 day ago
				sensor.TimeRelative = fmt.Sprintf("%d Hour%s ago", diff_hours, plural(diff_hours, "s"))
			} else {
				sensor.TimeRelative = fmt.Sprintf("%d Day%s", diff_days, plural(diff_days, "s"))
			}
		} else {
			// should hopefully not happen
			sensor.TimeRelative = sensor.Timestamp
		}

		data = append(data, sensor)
	}

	sort.Slice(data, func(i int, j int) bool {
		return data[i].Mac < data[j].Mac
	})

	// Render HTMX partial response
	tmpl := template.Must(template.ParseFiles("templates/sensors.html"))
	if err := tmpl.Execute(w, data); err != nil {
		log.Printf("%v", err)
		http.Error(w, "Error rendering data", http.StatusInternalServerError)
	}
}

func loadConfig() ConfigMap {
	var err error

	// Open the config.json file
	file, err := os.Open("../config.json")
	if err != nil {
		log.Fatalf("Failed to open config file: %v", err)
	}
	defer file.Close()

	// Read the file's contents
	data, err := io.ReadAll(file)
	if err != nil {
		log.Fatalf("Failed to read config file: %v", err)
	}

	// Parse the JSON into ConfigMap
	var config ConfigMap
	if err := json.Unmarshal(data, &config); err != nil {
		log.Fatalf("Failed to parse JSON: %v", err)
	}

	return config
}

func main() {
	// expect to run from mijia-root directory
	// var err error

	configMap = loadConfig()

	for mac, individualConfig := range configMap {
		// Connect to SQLite database
		db, err := sql.Open("sqlite3", fmt.Sprintf("../logs/%s.db", mac))
		if err != nil {
			log.Fatalf("Failed to connect to database: %v", err)
		}
		defer db.Close()

		individualConfig.Db = db
		configMap[mac] = individualConfig
	}
	fmt.Printf("%v", configMap)

	// Handle routes
	http.HandleFunc("/", renderHomePage)
	http.HandleFunc("/load_data", loadSensorData) // HTMX endpoint

	// Serve static files
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	// Start the server
	fmt.Println("Server is running on http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
