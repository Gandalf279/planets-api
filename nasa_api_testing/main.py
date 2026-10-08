import requests
import datetime as dt
import json
from pyproj import Transformer


BODY_ID         = 499 # Mars
CENTER  = "coord@399" # use coordinates specified in SITE_COORD on 399 (EARTH)
START_TIME      = dt.date.today()                           # Today
STOP_TIME       = dt.date.today() + dt.timedelta(days=1)    # Tomorrow
STEP_SIZE       = "1 h"                                    # d, h, m, y, mo

t = Transformer.from_crs("EPSG:9518", "EPSG:4979", always_xy=True)

lon, lat, H = 8.68, 50.11, 100.0
lon, lat, h = t.transform(lon, lat, H)
print(h)
SITE_COORD = f"{lon},{lat},{h}"
print(SITE_COORD)

r = requests.get(f"https://ssd.jpl.nasa.gov/api/horizons.api?format=text&MAKE_EPHEM=YES&COMMAND={BODY_ID}&EPHEM_TYPE=OBSERVER&CENTER='{CENTER}'&COORD_TYPE=GEODETIC&SITE_COORD='{SITE_COORD}'&START_TIME='{START_TIME}'&STOP_TIME='{STOP_TIME}'&STEP_SIZE='{STEP_SIZE}'&QUANTITIES='4'&REF_SYSTEM='ICRF'&CAL_FORMAT='CAL'&CAL_TYPE='M'&TIME_DIGITS='MINUTES'&ANG_FORMAT='HMS'&APPARENT='REFRACTED'&RANGE_UNITS='AU'&SUPPRESS_RANGE_RATE='NO'&SKIP_DAYLT='NO'&SOLAR_ELONG='0,180'&EXTRA_PREC='NO'&R_T_S_ONLY='NO'&CSV_FORMAT='YES'&OBJ_DATA='YES'")
print(r.status_code)
response = r.text
open(f"response{dt.datetime.now()}.txt", "w").write(response)

lines = response.splitlines()
soe = lines.index("$$SOE") 
eoe = lines.index("$$EOE")

records = []
for line in lines[soe + 1 : eoe]:
    parts = [p.strip() for p in line.split(",")]
    date, solar_flag, lunar_flag, azimuth, elevation = parts[:5]
    records.append({
        "date": date,
        "solar_flag": solar_flag,
        "lunar_flag": lunar_flag,
        "azimuth": float(azimuth),
        "elevation": float(elevation),
    })

open(f"data_{dt.datetime.now()}.json", "w").write(json.dumps(records, indent=2))

