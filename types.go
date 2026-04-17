package main

type IPInfo struct {
	IPAddress   string `json:"IPAddress"`
	Location    string `json:"Location"`
	ISP         string `json:"ISP"`
	TorExit     bool   `json:"TorExit"`
	City        string `json:"City"`
	Country     string `json:"Country"`
	CountryCode string `json:"CountryCode"`
}
