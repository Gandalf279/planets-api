package main

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
)

type Item struct {
	SunFlag   string  `json:"sun"`
	MoonFlag  string  `json:"moon"`
	Elevation float32 `json:"elevation"`
	Azimuth   float32 `json:"azimuth"`
}

type RequestItem struct {
	BodyID      string `json:"bodyid"`
	ObserverLat string `json:"observerlat"`
	ObserverLon string `json:"observerlon"`
	StartTime   string `json:"starttime"`
	StopTime    string `json:"stoptime"`
	StepSize    string `json:"stepsize"`
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

	// public data, no auth needed
	router.GET("/planets", getData)

	// Start server on port 8080 (default)
	router.Run()
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
	resp, err := http.Get("https://ssd.jpl.nasa.gov/api/horizons.api?format=text&MAKE_EPHEM=YES&COMMAND={BODY_ID}&EPHEM_TYPE=OBSERVER&CENTER='coord@399'&COORD_TYPE=GEODETIC&SITE_COORD='{SITE_COORD}'&START_TIME='{START_TIME}'&STOP_TIME='{STOP_TIME}'&STEP_SIZE='{STEP_SIZE}'&QUANTITIES='4'&REF_SYSTEM='ICRF'&CAL_FORMAT='CAL'&CAL_TYPE='M'&TIME_DIGITS='MINUTES'&ANG_FORMAT='HMS'&APPARENT='REFRACTED'&RANGE_UNITS='AU'&SUPPRESS_RANGE_RATE='NO'&SKIP_DAYLT='NO'&SOLAR_ELONG='0,180'&EXTRA_PREC='NO'&R_T_S_ONLY='NO'&CSV_FORMAT='YES'&OBJ_DATA='YES'")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		println("d")
		return
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	println(string(body))
}
