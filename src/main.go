package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

type Item struct {
	Date      string  `json:"date"`
	SunFlag   string  `json:"solarPresence"`
	MoonFlag  string  `json:"lunarPresence"`
	Azimuth   float64 `json:"azimuth"`
	Elevation float64 `json:"elevation"`
}

type RequestItem struct {
	BodyID         string `json:"bodyid"`
	ObserverLat    string `json:"observerlat"`
	ObserverLon    string `json:"observerlon"`
	ObserverHeight string `json:"observerheight"`
	StartTime      string `json:"starttime"`
	StopTime       string `json:"stoptime"`
	StepSize       string `json:"stepsize"`
}

func main() {
	router := gin.New()

	router.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Origin, Content-Type, Authorization")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})

	router.GET("/planets", getData)
	router.GET("/", sendHelp)
	router.GET("/help", sendHelp)
	router.GET("/planets_help", sendPlanetsHelp)

	router.Run()
}
func parseRows(rows [][]string) ([]Item, error) {
	items := make([]Item, 0, len(rows))
	for i, r := range rows {
		if len(r) < 5 {
			return nil, fmt.Errorf("row %d: too few fields: %q", i, r)
		}

		az, err := strconv.ParseFloat(strings.TrimSpace(r[3]), 64)
		if err != nil {
			return nil, fmt.Errorf("row %d azimuth: %w", i, err)
		}
		el, err := strconv.ParseFloat(strings.TrimSpace(r[4]), 64)
		if err != nil {
			return nil, fmt.Errorf("row %d elevation: %w", i, err)
		}

		items = append(items, Item{
			Date:      strings.TrimSpace(r[0]),
			SunFlag:   strings.TrimSpace(r[1]),
			MoonFlag:  strings.TrimSpace(r[2]),
			Azimuth:   az,
			Elevation: el,
		})
	}
	return items, nil
}

func sendHelp(c *gin.Context) {
	c.String(http.StatusOK, "Endpoints: \n\t '/': Send this help message \n\t '/help': Same as '/' \n\t '/planets': See example usage at \n\t\t '/planets_help'")
}

func sendPlanetsHelp(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"request": gin.H{
			"bodyid":         "499",
			"observerlat":    "12.345",
			"observerlon":    "67.891",
			"observerheight": "0.014",
			"starttime":      "2026-09-29",
			"stoptime":       "2026-09-30",
			"stepsize":       "1h",
		},
		"response": []Item{{Date: "2026-Sep-29 00:00", MoonFlag: "m", Azimuth: 64.178769, Elevation: 6.162181}},
	})
}

func getData(c *gin.Context) {
	var data RequestItem
	println("a")
	if err := c.BindJSON(&data); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		println("b")
		return
	}
	println("c")
	observerCoords := fmt.Sprintf("%s,%s,%s", data.ObserverLon, data.ObserverLat, data.ObserverHeight)
	url := fmt.Sprintf("https://ssd.jpl.nasa.gov/api/horizons.api?format=text&MAKE_EPHEM=YES&COMMAND=%s&EPHEM_TYPE=OBSERVER&CENTER='coord@399'&COORD_TYPE=GEODETIC&SITE_COORD='%s'&START_TIME='%s'&STOP_TIME='%s'&STEP_SIZE='%s'&QUANTITIES='4'&REF_SYSTEM='ICRF'&CAL_FORMAT='CAL'&CAL_TYPE='M'&TIME_DIGITS='MINUTES'&ANG_FORMAT='HMS'&APPARENT='REFRACTED'&RANGE_UNITS='AU'&SUPPRESS_RANGE_RATE='NO'&SKIP_DAYLT='NO'&SOLAR_ELONG='0,180'&EXTRA_PREC='NO'&R_T_S_ONLY='NO'&CSV_FORMAT='YES'&OBJ_DATA='YES'", data.BodyID, observerCoords, data.StartTime, data.StopTime, data.StepSize)
	resp, err := http.Get(url)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		println("d")
		return
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	println(string(body))

	lines := strings.Split(string(body), "\n")
	soe := slices.Index(lines, "$$SOE")
	eoe := slices.Index(lines, "$$EOE")
	fmt.Println("SOE: ", soe)
	fmt.Println("EOE: ", eoe)

	var records = make([]string, 0, eoe-soe)
	for idx, val := range lines {
		if idx > soe {
			if idx < eoe {
				fmt.Printf("%v\t%v\n", idx, val)
				records = append(records, val)
			}
		}
	}

	fmt.Println(records, "\n\n\n", records[1])

	r := csv.NewReader(strings.NewReader(strings.Join(lines[soe+1:eoe], "\n")))
	r.TrimLeadingSpace = true
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	fmt.Print("\n\n\n", rows)

	a, err := parseRows(rows)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		fmt.Println(fmt.Errorf("Error while parsing rows: %w", err))
		return
	}
	fmt.Println("\n\n\n", a)
	fmt.Println(reflect.TypeOf(a))

	c.JSON(http.StatusOK, a)

}
