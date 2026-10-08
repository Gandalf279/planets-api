package main

import (
	"archive/tar"
	"bufio"
	"compress/bzip2"
	"encoding/binary"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	gridFile    = "egm96_15.bin"
	gridRows    = 721
	gridCols    = 1441 // 1440 + wrap-around column
	gridStepDeg = 0.25

	// GeographicLib's distribution of EGM96 at 15' as a PGM image.
	geoidURL = "https://downloads.sourceforge.net/project/geographiclib/geoids-distrib/egm96-15.tar.bz2"
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

// HeightRequestItem: Height is meters above mean sea level.
type HeightRequestItem struct {
	Lat    string `json:"lat"`
	Lon    string `json:"lon"`
	Height string `json:"height"`
}

var geoidGrid *Grid

func main() {
	downloadOnly := flag.Bool("download-only", false, "download/convert the geoid grid and exit")
	flag.Parse()

	if err := ensureGridFile(gridFile); err != nil {
		log.Fatalf("preparing geoid grid: %v", err)
	}
	if *downloadOnly {
		fmt.Println("Geoid grid ready:", gridFile)
		return
	}

	var err error
	geoidGrid, err = LoadGrid(gridFile, gridRows, gridCols, gridStepDeg)
	if err != nil {
		log.Fatalf("loading geoid grid: %v", err)
	}

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
	router.GET("/height_conversion", convertHeight)

	router.Run()
}

// ---------------------------------------------------------------------------
// Geoid grid: download, convert, load, interpolate
// ---------------------------------------------------------------------------

// ensureGridFile makes sure the .bin exists, downloading and converting if not.
func ensureGridFile(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	log.Printf("%s not found, downloading from %s", path, geoidURL)

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(geoidURL)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: HTTP %s", resp.Status)
	}

	// tar.bz2 -> find the .pgm entry
	tr := tar.NewReader(bzip2.NewReader(resp.Body))
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return errors.New("no .pgm file found in archive")
		}
		if err != nil {
			return fmt.Errorf("reading archive: %w", err)
		}
		if strings.HasSuffix(hdr.Name, ".pgm") {
			vals, err := readPGM(tr)
			if err != nil {
				return fmt.Errorf("parsing pgm: %w", err)
			}
			if err := writeBin(path, vals); err != nil {
				return fmt.Errorf("writing %s: %w", path, err)
			}
			log.Printf("wrote %s", path)
			return nil
		}
	}
}

// readPGM parses GeographicLib's 16-bit big-endian P5 geoid image.
// Height = Offset + Scale * pixel. Rows go north->south, columns 0°->360°.
// A wrap-around column is appended so the result is 721 x 1441.
func readPGM(r io.Reader) ([][]float64, error) {
	br := bufio.NewReader(r)
	var tokens []string
	offset, scale := 0.0, 1.0
	haveOffset, haveScale := false, false

	for len(tokens) < 4 { // P5, width, height, maxval
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("reading header: %w", err)
		}
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			f := strings.Fields(strings.TrimPrefix(line, "#"))
			if len(f) >= 2 {
				switch f[0] {
				case "Offset":
					offset, err = strconv.ParseFloat(f[1], 64)
					haveOffset = err == nil
				case "Scale":
					scale, err = strconv.ParseFloat(f[1], 64)
					haveScale = err == nil
				}
			}
			continue
		}
		tokens = append(tokens, strings.Fields(line)...)
	}
	if tokens[0] != "P5" {
		return nil, fmt.Errorf("unexpected magic %q", tokens[0])
	}
	w, err1 := strconv.Atoi(tokens[1])
	h, err2 := strconv.Atoi(tokens[2])
	maxv, err3 := strconv.Atoi(tokens[3])
	if err1 != nil || err2 != nil || err3 != nil {
		return nil, errors.New("bad PGM dimensions")
	}
	if w != gridCols-1 || h != gridRows || maxv != 65535 {
		return nil, fmt.Errorf("unexpected PGM layout %dx%d max %d", w, h, maxv)
	}
	if !haveOffset || !haveScale {
		return nil, errors.New("PGM header missing Offset/Scale")
	}

	raw := make([]byte, w*h*2)
	if _, err := io.ReadFull(br, raw); err != nil {
		return nil, fmt.Errorf("reading pixels: %w", err)
	}

	vals := make([][]float64, h)
	for row := 0; row < h; row++ {
		vals[row] = make([]float64, w+1)
		for col := 0; col < w; col++ {
			p := binary.BigEndian.Uint16(raw[(row*w+col)*2:])
			vals[row][col] = offset + scale*float64(p)
		}
		vals[row][w] = vals[row][0] // wrap 360° = 0°
	}
	return vals, nil
}

// writeBin stores the grid as little-endian float32, row-major.
func writeBin(path string, vals [][]float64) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	bw := bufio.NewWriter(f)
	buf := make([]byte, 4)
	for _, row := range vals {
		for _, v := range row {
			binary.LittleEndian.PutUint32(buf, math.Float32bits(float32(v)))
			if _, err := bw.Write(buf); err != nil {
				f.Close()
				return err
			}
		}
	}
	if err := bw.Flush(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Grid is a regular global geoid grid. Values[row][col], row 0 = +90° lat,
// col 0 = 0° lon, increasing eastward (EGM96 15' layout).
type Grid struct {
	Values  [][]float64 // geoid undulation N in meters
	StepDeg float64
}

// LoadGrid reads a flat file of rows*cols little-endian float32 values (meters).
func LoadGrid(path string, rows, cols int, stepDeg float64) (*Grid, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) != rows*cols*4 {
		return nil, fmt.Errorf("unexpected file size %d, want %d (delete %s to re-download)", len(raw), rows*cols*4, path)
	}
	vals := make([][]float64, rows)
	for r := 0; r < rows; r++ {
		vals[r] = make([]float64, cols)
		for c := 0; c < cols; c++ {
			bits := binary.LittleEndian.Uint32(raw[(r*cols+c)*4:])
			vals[r][c] = float64(math.Float32frombits(bits))
		}
	}
	return &Grid{Values: vals, StepDeg: stepDeg}, nil
}

// Undulation returns N at the given lat/lon (degrees) via bilinear interpolation.
func (g *Grid) Undulation(latDeg, lonDeg float64) (float64, error) {
	if latDeg < -90 || latDeg > 90 || math.IsNaN(latDeg) || math.IsNaN(lonDeg) {
		return 0, errors.New("latitude out of range")
	}
	lonDeg = math.Mod(lonDeg, 360)
	if lonDeg < 0 {
		lonDeg += 360
	}

	rows := len(g.Values)
	cols := len(g.Values[0])

	fr := (90 - latDeg) / g.StepDeg
	fc := lonDeg / g.StepDeg

	r0 := int(math.Floor(fr))
	if r0 >= rows-1 {
		r0 = rows - 2
	}
	c0 := int(math.Floor(fc))
	if c0 >= cols-1 {
		c0 = cols - 2
	}
	dr := fr - float64(r0)
	dc := fc - float64(c0)

	n00 := g.Values[r0][c0]
	n01 := g.Values[r0][c0+1]
	n10 := g.Values[r0+1][c0]
	n11 := g.Values[r0+1][c0+1]

	top := n00*(1-dc) + n01*dc
	bot := n10*(1-dc) + n11*dc
	return top*(1-dr) + bot*dr, nil
}

// MSLToEllipsoidal converts orthometric height H (above MSL) to height above the ellipsoid.
func (g *Grid) MSLToEllipsoidal(latDeg, lonDeg, hMSL float64) (float64, error) {
	n, err := g.Undulation(latDeg, lonDeg)
	if err != nil {
		return 0, err
	}
	return hMSL + n, nil
}

// ---------------------------------------------------------------------------
// HTTP handlers
// ---------------------------------------------------------------------------

func sendHelp(c *gin.Context) {
	c.String(http.StatusOK, "Endpoints: \n"+
		"\t '/': Send this help message \n"+
		"\t '/help': Same as '/' \n"+
		"\t '/planets': See example usage at '/planets_help' \n"+
		"\t '/height_conversion': JSON body {\"lat\":\"48.78\",\"lon\":\"9.18\",\"height\":\"250\"} (height in meters above MSL)")
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

func parseFloatField(name, s string) (float64, error) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", name, err)
	}
	return v, nil
}

func convertHeight(c *gin.Context) {
	var data HeightRequestItem
	if err := c.BindJSON(&data); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	lat, err := parseFloatField("lat", data.Lat)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	lon, err := parseFloatField("lon", data.Lon)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	hMSL, err := parseFloatField("height", data.Height)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	n, err := geoidGrid.Undulation(lat, lon)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	hEll := hMSL + n

	c.JSON(http.StatusOK, gin.H{
		"lat":                   lat,
		"lon":                   lon,
		"undulationMeters":      n,
		"heightMSLMeters":       hMSL,
		"heightEllipsoidMeters": hEll,
		"heightEllipsoidKm":     hEll / 1000, // Horizons SITE_COORD expects km
	})
}

func getData(c *gin.Context) {
	var data RequestItem
	if err := c.BindJSON(&data); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	observerCoords := fmt.Sprintf("%s,%s,%s", data.ObserverLon, data.ObserverLat, data.ObserverHeight)
	url := fmt.Sprintf("https://ssd.jpl.nasa.gov/api/horizons.api?format=text&MAKE_EPHEM=YES&COMMAND=%s&EPHEM_TYPE=OBSERVER&CENTER='coord@399'&COORD_TYPE=GEODETIC&SITE_COORD='%s'&START_TIME='%s'&STOP_TIME='%s'&STEP_SIZE='%s'&QUANTITIES='4'&REF_SYSTEM='ICRF'&CAL_FORMAT='CAL'&CAL_TYPE='M'&TIME_DIGITS='MINUTES'&ANG_FORMAT='HMS'&APPARENT='REFRACTED'&RANGE_UNITS='AU'&SUPPRESS_RANGE_RATE='NO'&SKIP_DAYLT='NO'&SOLAR_ELONG='0,180'&EXTRA_PREC='NO'&R_T_S_ONLY='NO'&CSV_FORMAT='YES'&OBJ_DATA='YES'",
		data.BodyID, observerCoords, data.StartTime, data.StopTime, data.StepSize)

	resp, err := http.Get(url)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	lines := strings.Split(string(body), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], "\r")
	}
	soe := slices.Index(lines, "$$SOE")
	eoe := slices.Index(lines, "$$EOE")
	if soe < 0 || eoe <= soe {
		c.JSON(http.StatusBadGateway, gin.H{
			"error":   "unexpected response from Horizons (no $$SOE/$$EOE markers)",
			"details": string(body),
		})
		return
	}

	r := csv.NewReader(strings.NewReader(strings.Join(lines[soe+1:eoe], "\n")))
	r.TrimLeadingSpace = true
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	items, err := parseRows(rows)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, items)
}
