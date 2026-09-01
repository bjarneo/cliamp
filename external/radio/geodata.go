// Code generated from tzdata zone.tab; DO NOT EDIT.
// Regenerate with: go generate ./external/radio

package radio

// regionCodes groups ISO 3166-1 alpha-2 country codes into the six broad
// regions the country list is grouped by. Derived from the timezone area of
// each country's zone.tab rows, with hand overrides where that area is an
// ocean (Atlantic, Indian, Arctic) rather than a region, and for
// transcontinental countries, where the region holding most of the
// population wins.
var regionCodes = map[string]string{
	"Africa":     "AO BF BI BJ BW CD CF CG CI CM CV DJ DZ EG EH ER ET GA GH GM GN GQ GW KE KM LR LS LY MA MG ML MR MU MW MZ NA NE NG RE RW SC SD SH SL SN SO SS ST SZ TD TG TN TZ UG YT ZA ZM ZW",
	"Americas":   "AG AI AR AW BB BL BM BO BQ BR BS BZ CA CL CO CR CU CW DM DO EC FK GD GF GL GP GS GT GY HN HT JM KN KY LC MF MQ MS MX NI PA PE PM PR PY SR SV SX TC TT US UY VC VE VG VI",
	"Asia":       "AE AF AM AZ BD BH BN BT CN CY GE HK ID IL IN IO IQ IR JO JP KG KH KP KR KW KZ LA LB LK MM MN MO MV MY NP OM PH PK PS QA SA SG SY TH TJ TL TM TW UZ VN YE",
	"Europe":     "AD AL AT AX BA BE BG BY CH CZ DE DK EE ES FI FO FR GB GG GI GR HR HU IE IM IS IT JE LI LT LU LV MC MD ME MK MT NL NO PL PT RO RS RU SE SI SJ SK SM TR UA VA XK",
	"Oceania":    "AS AU CC CK CX FJ FM GU KI MH MP NC NF NR NU NZ PF PG PN PW SB TK TO TV UM VU WF WS",
	"Antarctica": "AQ TF",
}

// zoneCountries maps IANA timezone names to the ISO 3166-1 alpha-2 code of
// the country that owns them, one "Zone CC" pair per line. Taken from
// tzdata's zone.tab, whose first column is exactly one country code.
const zoneCountries = `
Africa/Abidjan CI
Africa/Accra GH
Africa/Addis_Ababa ET
Africa/Algiers DZ
Africa/Asmara ER
Africa/Bamako ML
Africa/Bangui CF
Africa/Banjul GM
Africa/Bissau GW
Africa/Blantyre MW
Africa/Brazzaville CG
Africa/Bujumbura BI
Africa/Cairo EG
Africa/Casablanca MA
Africa/Ceuta ES
Africa/Conakry GN
Africa/Dakar SN
Africa/Dar_es_Salaam TZ
Africa/Djibouti DJ
Africa/Douala CM
Africa/El_Aaiun EH
Africa/Freetown SL
Africa/Gaborone BW
Africa/Harare ZW
Africa/Johannesburg ZA
Africa/Juba SS
Africa/Kampala UG
Africa/Khartoum SD
Africa/Kigali RW
Africa/Kinshasa CD
Africa/Lagos NG
Africa/Libreville GA
Africa/Lome TG
Africa/Luanda AO
Africa/Lubumbashi CD
Africa/Lusaka ZM
Africa/Malabo GQ
Africa/Maputo MZ
Africa/Maseru LS
Africa/Mbabane SZ
Africa/Mogadishu SO
Africa/Monrovia LR
Africa/Nairobi KE
Africa/Ndjamena TD
Africa/Niamey NE
Africa/Nouakchott MR
Africa/Ouagadougou BF
Africa/Porto-Novo BJ
Africa/Sao_Tome ST
Africa/Tripoli LY
Africa/Tunis TN
Africa/Windhoek NA
America/Adak US
America/Anchorage US
America/Anguilla AI
America/Antigua AG
America/Araguaina BR
America/Argentina/Buenos_Aires AR
America/Argentina/Catamarca AR
America/Argentina/Cordoba AR
America/Argentina/Jujuy AR
America/Argentina/La_Rioja AR
America/Argentina/Mendoza AR
America/Argentina/Rio_Gallegos AR
America/Argentina/Salta AR
America/Argentina/San_Juan AR
America/Argentina/San_Luis AR
America/Argentina/Tucuman AR
America/Argentina/Ushuaia AR
America/Aruba AW
America/Asuncion PY
America/Atikokan CA
America/Bahia BR
America/Bahia_Banderas MX
America/Barbados BB
America/Belem BR
America/Belize BZ
America/Blanc-Sablon CA
America/Boa_Vista BR
America/Bogota CO
America/Boise US
America/Cambridge_Bay CA
America/Campo_Grande BR
America/Cancun MX
America/Caracas VE
America/Cayenne GF
America/Cayman KY
America/Chicago US
America/Chihuahua MX
America/Ciudad_Juarez MX
America/Costa_Rica CR
America/Coyhaique CL
America/Creston CA
America/Cuiaba BR
America/Curacao CW
America/Danmarkshavn GL
America/Dawson CA
America/Dawson_Creek CA
America/Denver US
America/Detroit US
America/Dominica DM
America/Edmonton CA
America/Eirunepe BR
America/El_Salvador SV
America/Fort_Nelson CA
America/Fortaleza BR
America/Glace_Bay CA
America/Goose_Bay CA
America/Grand_Turk TC
America/Grenada GD
America/Guadeloupe GP
America/Guatemala GT
America/Guayaquil EC
America/Guyana GY
America/Halifax CA
America/Havana CU
America/Hermosillo MX
America/Indiana/Indianapolis US
America/Indiana/Knox US
America/Indiana/Marengo US
America/Indiana/Petersburg US
America/Indiana/Tell_City US
America/Indiana/Vevay US
America/Indiana/Vincennes US
America/Indiana/Winamac US
America/Inuvik CA
America/Iqaluit CA
America/Jamaica JM
America/Juneau US
America/Kentucky/Louisville US
America/Kentucky/Monticello US
America/Kralendijk BQ
America/La_Paz BO
America/Lima PE
America/Los_Angeles US
America/Lower_Princes SX
America/Maceio BR
America/Managua NI
America/Manaus BR
America/Marigot MF
America/Martinique MQ
America/Matamoros MX
America/Mazatlan MX
America/Menominee US
America/Merida MX
America/Metlakatla US
America/Mexico_City MX
America/Miquelon PM
America/Moncton CA
America/Monterrey MX
America/Montevideo UY
America/Montserrat MS
America/Nassau BS
America/New_York US
America/Nome US
America/Noronha BR
America/North_Dakota/Beulah US
America/North_Dakota/Center US
America/North_Dakota/New_Salem US
America/Nuuk GL
America/Ojinaga MX
America/Panama PA
America/Paramaribo SR
America/Phoenix US
America/Port-au-Prince HT
America/Port_of_Spain TT
America/Porto_Velho BR
America/Puerto_Rico PR
America/Punta_Arenas CL
America/Rankin_Inlet CA
America/Recife BR
America/Regina CA
America/Resolute CA
America/Rio_Branco BR
America/Santarem BR
America/Santiago CL
America/Santo_Domingo DO
America/Sao_Paulo BR
America/Scoresbysund GL
America/Sitka US
America/St_Barthelemy BL
America/St_Johns CA
America/St_Kitts KN
America/St_Lucia LC
America/St_Thomas VI
America/St_Vincent VC
America/Swift_Current CA
America/Tegucigalpa HN
America/Thule GL
America/Tijuana MX
America/Toronto CA
America/Tortola VG
America/Vancouver CA
America/Whitehorse CA
America/Winnipeg CA
America/Yakutat US
Antarctica/Casey AQ
Antarctica/Davis AQ
Antarctica/DumontDUrville AQ
Antarctica/Macquarie AU
Antarctica/Mawson AQ
Antarctica/McMurdo AQ
Antarctica/Palmer AQ
Antarctica/Rothera AQ
Antarctica/Syowa AQ
Antarctica/Troll AQ
Antarctica/Vostok AQ
Arctic/Longyearbyen SJ
Asia/Aden YE
Asia/Almaty KZ
Asia/Amman JO
Asia/Anadyr RU
Asia/Aqtau KZ
Asia/Aqtobe KZ
Asia/Ashgabat TM
Asia/Atyrau KZ
Asia/Baghdad IQ
Asia/Bahrain BH
Asia/Baku AZ
Asia/Bangkok TH
Asia/Barnaul RU
Asia/Beirut LB
Asia/Bishkek KG
Asia/Brunei BN
Asia/Chita RU
Asia/Colombo LK
Asia/Damascus SY
Asia/Dhaka BD
Asia/Dili TL
Asia/Dubai AE
Asia/Dushanbe TJ
Asia/Famagusta CY
Asia/Gaza PS
Asia/Hebron PS
Asia/Ho_Chi_Minh VN
Asia/Hong_Kong HK
Asia/Hovd MN
Asia/Irkutsk RU
Asia/Jakarta ID
Asia/Jayapura ID
Asia/Jerusalem IL
Asia/Kabul AF
Asia/Kamchatka RU
Asia/Karachi PK
Asia/Kathmandu NP
Asia/Khandyga RU
Asia/Kolkata IN
Asia/Krasnoyarsk RU
Asia/Kuala_Lumpur MY
Asia/Kuching MY
Asia/Kuwait KW
Asia/Macau MO
Asia/Magadan RU
Asia/Makassar ID
Asia/Manila PH
Asia/Muscat OM
Asia/Nicosia CY
Asia/Novokuznetsk RU
Asia/Novosibirsk RU
Asia/Omsk RU
Asia/Oral KZ
Asia/Phnom_Penh KH
Asia/Pontianak ID
Asia/Pyongyang KP
Asia/Qatar QA
Asia/Qostanay KZ
Asia/Qyzylorda KZ
Asia/Riyadh SA
Asia/Sakhalin RU
Asia/Samarkand UZ
Asia/Seoul KR
Asia/Shanghai CN
Asia/Singapore SG
Asia/Srednekolymsk RU
Asia/Taipei TW
Asia/Tashkent UZ
Asia/Tbilisi GE
Asia/Tehran IR
Asia/Thimphu BT
Asia/Tokyo JP
Asia/Tomsk RU
Asia/Ulaanbaatar MN
Asia/Urumqi CN
Asia/Ust-Nera RU
Asia/Vientiane LA
Asia/Vladivostok RU
Asia/Yakutsk RU
Asia/Yangon MM
Asia/Yekaterinburg RU
Asia/Yerevan AM
Atlantic/Azores PT
Atlantic/Bermuda BM
Atlantic/Canary ES
Atlantic/Cape_Verde CV
Atlantic/Faroe FO
Atlantic/Madeira PT
Atlantic/Reykjavik IS
Atlantic/South_Georgia GS
Atlantic/St_Helena SH
Atlantic/Stanley FK
Australia/Adelaide AU
Australia/Brisbane AU
Australia/Broken_Hill AU
Australia/Darwin AU
Australia/Eucla AU
Australia/Hobart AU
Australia/Lindeman AU
Australia/Lord_Howe AU
Australia/Melbourne AU
Australia/Perth AU
Australia/Sydney AU
Europe/Amsterdam NL
Europe/Andorra AD
Europe/Astrakhan RU
Europe/Athens GR
Europe/Belgrade RS
Europe/Berlin DE
Europe/Bratislava SK
Europe/Brussels BE
Europe/Bucharest RO
Europe/Budapest HU
Europe/Busingen DE
Europe/Chisinau MD
Europe/Copenhagen DK
Europe/Dublin IE
Europe/Gibraltar GI
Europe/Guernsey GG
Europe/Helsinki FI
Europe/Isle_of_Man IM
Europe/Istanbul TR
Europe/Jersey JE
Europe/Kaliningrad RU
Europe/Kirov RU
Europe/Kyiv UA
Europe/Lisbon PT
Europe/Ljubljana SI
Europe/London GB
Europe/Luxembourg LU
Europe/Madrid ES
Europe/Malta MT
Europe/Mariehamn AX
Europe/Minsk BY
Europe/Monaco MC
Europe/Moscow RU
Europe/Oslo NO
Europe/Paris FR
Europe/Podgorica ME
Europe/Prague CZ
Europe/Riga LV
Europe/Rome IT
Europe/Samara RU
Europe/San_Marino SM
Europe/Sarajevo BA
Europe/Saratov RU
Europe/Simferopol UA
Europe/Skopje MK
Europe/Sofia BG
Europe/Stockholm SE
Europe/Tallinn EE
Europe/Tirane AL
Europe/Ulyanovsk RU
Europe/Vaduz LI
Europe/Vatican VA
Europe/Vienna AT
Europe/Vilnius LT
Europe/Volgograd RU
Europe/Warsaw PL
Europe/Zagreb HR
Europe/Zurich CH
Indian/Antananarivo MG
Indian/Chagos IO
Indian/Christmas CX
Indian/Cocos CC
Indian/Comoro KM
Indian/Kerguelen TF
Indian/Mahe SC
Indian/Maldives MV
Indian/Mauritius MU
Indian/Mayotte YT
Indian/Reunion RE
Pacific/Apia WS
Pacific/Auckland NZ
Pacific/Bougainville PG
Pacific/Chatham NZ
Pacific/Chuuk FM
Pacific/Easter CL
Pacific/Efate VU
Pacific/Fakaofo TK
Pacific/Fiji FJ
Pacific/Funafuti TV
Pacific/Galapagos EC
Pacific/Gambier PF
Pacific/Guadalcanal SB
Pacific/Guam GU
Pacific/Honolulu US
Pacific/Kanton KI
Pacific/Kiritimati KI
Pacific/Kosrae FM
Pacific/Kwajalein MH
Pacific/Majuro MH
Pacific/Marquesas PF
Pacific/Midway UM
Pacific/Nauru NR
Pacific/Niue NU
Pacific/Norfolk NF
Pacific/Noumea NC
Pacific/Pago_Pago AS
Pacific/Palau PW
Pacific/Pitcairn PN
Pacific/Pohnpei FM
Pacific/Port_Moresby PG
Pacific/Rarotonga CK
Pacific/Saipan MP
Pacific/Tahiti PF
Pacific/Tarawa KI
Pacific/Tongatapu TO
Pacific/Wake UM
Pacific/Wallis WF
`

// countryNames gives every country code a readable name without a network
// call, so the pane can say "Norway" before the directory's own country
// index has loaded. Taken from tzdata's iso3166.tab, whose names are
// deliberately short ("Britain (UK)", not the directory's full legal title).
var countryNames = map[string]string{
	"AD": "Andorra",
	"AE": "United Arab Emirates",
	"AF": "Afghanistan",
	"AG": "Antigua & Barbuda",
	"AI": "Anguilla",
	"AL": "Albania",
	"AM": "Armenia",
	"AO": "Angola",
	"AQ": "Antarctica",
	"AR": "Argentina",
	"AS": "Samoa (American)",
	"AT": "Austria",
	"AU": "Australia",
	"AW": "Aruba",
	"AX": "Åland Islands",
	"AZ": "Azerbaijan",
	"BA": "Bosnia & Herzegovina",
	"BB": "Barbados",
	"BD": "Bangladesh",
	"BE": "Belgium",
	"BF": "Burkina Faso",
	"BG": "Bulgaria",
	"BH": "Bahrain",
	"BI": "Burundi",
	"BJ": "Benin",
	"BL": "St Barthelemy",
	"BM": "Bermuda",
	"BN": "Brunei",
	"BO": "Bolivia",
	"BQ": "Caribbean NL",
	"BR": "Brazil",
	"BS": "Bahamas",
	"BT": "Bhutan",
	"BV": "Bouvet Island",
	"BW": "Botswana",
	"BY": "Belarus",
	"BZ": "Belize",
	"CA": "Canada",
	"CC": "Cocos (Keeling) Islands",
	"CD": "Congo (Dem. Rep.)",
	"CF": "Central African Rep.",
	"CG": "Congo (Rep.)",
	"CH": "Switzerland",
	"CI": "Côte d’Ivoire",
	"CK": "Cook Islands",
	"CL": "Chile",
	"CM": "Cameroon",
	"CN": "China",
	"CO": "Colombia",
	"CR": "Costa Rica",
	"CU": "Cuba",
	"CV": "Cape Verde",
	"CW": "Curaçao",
	"CX": "Christmas Island",
	"CY": "Cyprus",
	"CZ": "Czech Republic",
	"DE": "Germany",
	"DJ": "Djibouti",
	"DK": "Denmark",
	"DM": "Dominica",
	"DO": "Dominican Republic",
	"DZ": "Algeria",
	"EC": "Ecuador",
	"EE": "Estonia",
	"EG": "Egypt",
	"EH": "Western Sahara",
	"ER": "Eritrea",
	"ES": "Spain",
	"ET": "Ethiopia",
	"FI": "Finland",
	"FJ": "Fiji",
	"FK": "Falkland Islands",
	"FM": "Micronesia",
	"FO": "Faroe Islands",
	"FR": "France",
	"GA": "Gabon",
	"GB": "Britain (UK)",
	"GD": "Grenada",
	"GE": "Georgia",
	"GF": "French Guiana",
	"GG": "Guernsey",
	"GH": "Ghana",
	"GI": "Gibraltar",
	"GL": "Greenland",
	"GM": "Gambia",
	"GN": "Guinea",
	"GP": "Guadeloupe",
	"GQ": "Equatorial Guinea",
	"GR": "Greece",
	"GS": "South Georgia & the South Sandwich Islands",
	"GT": "Guatemala",
	"GU": "Guam",
	"GW": "Guinea-Bissau",
	"GY": "Guyana",
	"HK": "Hong Kong",
	"HM": "Heard Island & McDonald Islands",
	"HN": "Honduras",
	"HR": "Croatia",
	"HT": "Haiti",
	"HU": "Hungary",
	"ID": "Indonesia",
	"IE": "Ireland",
	"IL": "Israel",
	"IM": "Isle of Man",
	"IN": "India",
	"IO": "British Indian Ocean Territory",
	"IQ": "Iraq",
	"IR": "Iran",
	"IS": "Iceland",
	"IT": "Italy",
	"JE": "Jersey",
	"JM": "Jamaica",
	"JO": "Jordan",
	"JP": "Japan",
	"KE": "Kenya",
	"KG": "Kyrgyzstan",
	"KH": "Cambodia",
	"KI": "Kiribati",
	"KM": "Comoros",
	"KN": "St Kitts & Nevis",
	"KP": "Korea (North)",
	"KR": "Korea (South)",
	"KW": "Kuwait",
	"KY": "Cayman Islands",
	"KZ": "Kazakhstan",
	"LA": "Laos",
	"LB": "Lebanon",
	"LC": "St Lucia",
	"LI": "Liechtenstein",
	"LK": "Sri Lanka",
	"LR": "Liberia",
	"LS": "Lesotho",
	"LT": "Lithuania",
	"LU": "Luxembourg",
	"LV": "Latvia",
	"LY": "Libya",
	"MA": "Morocco",
	"MC": "Monaco",
	"MD": "Moldova",
	"ME": "Montenegro",
	"MF": "St Martin (French)",
	"MG": "Madagascar",
	"MH": "Marshall Islands",
	"MK": "North Macedonia",
	"ML": "Mali",
	"MM": "Myanmar (Burma)",
	"MN": "Mongolia",
	"MO": "Macau",
	"MP": "Northern Mariana Islands",
	"MQ": "Martinique",
	"MR": "Mauritania",
	"MS": "Montserrat",
	"MT": "Malta",
	"MU": "Mauritius",
	"MV": "Maldives",
	"MW": "Malawi",
	"MX": "Mexico",
	"MY": "Malaysia",
	"MZ": "Mozambique",
	"NA": "Namibia",
	"NC": "New Caledonia",
	"NE": "Niger",
	"NF": "Norfolk Island",
	"NG": "Nigeria",
	"NI": "Nicaragua",
	"NL": "Netherlands",
	"NO": "Norway",
	"NP": "Nepal",
	"NR": "Nauru",
	"NU": "Niue",
	"NZ": "New Zealand",
	"OM": "Oman",
	"PA": "Panama",
	"PE": "Peru",
	"PF": "French Polynesia",
	"PG": "Papua New Guinea",
	"PH": "Philippines",
	"PK": "Pakistan",
	"PL": "Poland",
	"PM": "St Pierre & Miquelon",
	"PN": "Pitcairn",
	"PR": "Puerto Rico",
	"PS": "Palestine",
	"PT": "Portugal",
	"PW": "Palau",
	"PY": "Paraguay",
	"QA": "Qatar",
	"RE": "Réunion",
	"RO": "Romania",
	"RS": "Serbia",
	"RU": "Russia",
	"RW": "Rwanda",
	"SA": "Saudi Arabia",
	"SB": "Solomon Islands",
	"SC": "Seychelles",
	"SD": "Sudan",
	"SE": "Sweden",
	"SG": "Singapore",
	"SH": "St Helena",
	"SI": "Slovenia",
	"SJ": "Svalbard & Jan Mayen",
	"SK": "Slovakia",
	"SL": "Sierra Leone",
	"SM": "San Marino",
	"SN": "Senegal",
	"SO": "Somalia",
	"SR": "Suriname",
	"SS": "South Sudan",
	"ST": "Sao Tome & Principe",
	"SV": "El Salvador",
	"SX": "St Maarten (Dutch)",
	"SY": "Syria",
	"SZ": "Eswatini (Swaziland)",
	"TC": "Turks & Caicos Is",
	"TD": "Chad",
	"TF": "French S. Terr.",
	"TG": "Togo",
	"TH": "Thailand",
	"TJ": "Tajikistan",
	"TK": "Tokelau",
	"TL": "East Timor",
	"TM": "Turkmenistan",
	"TN": "Tunisia",
	"TO": "Tonga",
	"TR": "Turkey",
	"TT": "Trinidad & Tobago",
	"TV": "Tuvalu",
	"TW": "Taiwan",
	"TZ": "Tanzania",
	"UA": "Ukraine",
	"UG": "Uganda",
	"UM": "US minor outlying islands",
	"US": "United States",
	"UY": "Uruguay",
	"UZ": "Uzbekistan",
	"VA": "Vatican City",
	"VC": "St Vincent",
	"VE": "Venezuela",
	"VG": "Virgin Islands (UK)",
	"VI": "Virgin Islands (US)",
	"VN": "Vietnam",
	"VU": "Vanuatu",
	"WF": "Wallis & Futuna",
	"WS": "Samoa (western)",
	"XK": "Kosovo",
	"YE": "Yemen",
	"YT": "Mayotte",
	"ZA": "South Africa",
	"ZM": "Zambia",
	"ZW": "Zimbabwe",
}
