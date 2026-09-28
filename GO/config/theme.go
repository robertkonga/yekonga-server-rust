package config

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
)

type HexColor string

// LoadTheme reads a JSON file and unmarshals it into a Theme struct
func LoadTheme[T any](data string) (*T, error) {
	// Unmarshal JSON into Theme struct
	var theme T
	if err := json.Unmarshal([]byte(data), &theme); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	return &theme, nil
}

// LoadTheme reads a JSON file and unmarshals it into a Theme struct
func LoadThemeFile(filePath string) (*Theme, error) {
	// filePath = getPath(filePath)
	// Open the JSON file
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Read the file contents
	bytes, err := ioutil.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	// Unmarshal JSON into Theme struct
	var theme Theme
	if err := json.Unmarshal(bytes, &theme); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	return &theme, nil
}

type TenantConfig struct {
	UserId            string     `json:"userId"`
	TenantId          string     `json:"tenantId"`
	Domain            string     `json:"domain"`
	TenantName        string     `json:"tenantName"`
	AppName           string     `json:"appName"`
	BaseUrl           string     `json:"baseUrl"`
	Description       string     `json:"description"`
	Language          string     `json:"language"`
	LogoUrl           string     `json:"logoUrl"`
	FaviconUrl        string     `json:"faviconUrl"`
	Type              string     `json:"type"`
	Address           string     `json:"address"`
	Email             string     `json:"email"`
	Phone             string     `json:"phone"`
	Smtp              any        `json:"smtp"`
	Sms               any        `json:"sms"`
	Whatsapp          any        `json:"whatsapp"`
	OtherConfig       any        `json:"otherConfig,omitempty"`
	LightTheme        LightTheme `json:"lightTheme"`
	DarkTheme         DartTheme  `json:"darkTheme"`
	HasMembership     bool       `json:"hasMembership"`
	PublicCanRegister bool       `json:"publicCanRegister"`
}

type Theme struct {
	LightTheme LightTheme `json:"lightTheme"`
	DarkTheme  DartTheme  `json:"darkTheme"`
}

type LightTheme struct {
	Logo       string           `json:"logo"`
	Semantic   SemanticColors   `json:"semantic"`
	Background BackgroundColors `json:"background"`
	Text       TextColors       `json:"text"`
	Border     BorderColors     `json:"border"`
	Outline    OutlineColors    `json:"outline"`
	SVG        SVGColors        `json:"svg"`
	Shadow     Shadows          `json:"shadow"`

	Radius Radius `json:"radius"`
	Fonts  Fonts  `json:"fonts"`
}

type DartTheme struct {
	Logo       string           `json:"logo"`
	Background BackgroundColors `json:"background"`
	Text       TextColors       `json:"text"`
	Border     BorderColors     `json:"border"`
	Outline    OutlineColors    `json:"outline"`
	SVG        SVGColors        `json:"svg"`
	Shadow     Shadows          `json:"shadow"`
}

type SemanticColors struct {
	Brand     HexColor `json:"brand"`
	Primary   HexColor `json:"primary"`
	Secondary HexColor `json:"secondary,omitempty"`
	Success   HexColor `json:"success"`
	Danger    HexColor `json:"danger"`
	Warning   HexColor `json:"warning"`
	Info      HexColor `json:"info"`
}

type BackgroundColors struct {
	App     HexColor `json:"app"`
	Body    HexColor `json:"body"`
	Sidebar HexColor `json:"sidebar"`
	Header  HexColor `json:"header"`
	Card    HexColor `json:"card"`
	Muted   HexColor `json:"muted"`
}

type TextColors struct {
	App           HexColor `json:"app"`
	Body          HexColor `json:"body"`
	BodyActive    HexColor `json:"body-active"`
	Sidebar       HexColor `json:"sidebar"`
	SidebarActive HexColor `json:"sidebar-active"`
	Header        HexColor `json:"header"`
	HeaderActive  HexColor `json:"header-active"`
	Card          HexColor `json:"card"`
	Muted         HexColor `json:"muted"`

	OnBrand     HexColor `json:"on-brand"`
	OnPrimary   HexColor `json:"on-primary"`
	OnSecondary HexColor `json:"on-secondary,omitempty"`
	OnSuccess   HexColor `json:"on-success"`
	OnDanger    HexColor `json:"on-danger"`
	OnWarning   HexColor `json:"on-warning"`
	OnInfo      HexColor `json:"on-info"`
}

type BorderColors struct {
	App     HexColor `json:"app"`
	Body    HexColor `json:"body"`
	Sidebar HexColor `json:"sidebar"`
	Header  HexColor `json:"header"`
	Card    HexColor `json:"card"`
	Muted   HexColor `json:"muted"`
	Default HexColor `json:"default"`
}

type OutlineColors struct {
	App     HexColor `json:"app"`
	Body    HexColor `json:"body"`
	Sidebar HexColor `json:"sidebar"`
	Header  HexColor `json:"header"`
	Card    HexColor `json:"card"`
	Muted   HexColor `json:"muted"`
}

type SVGColors struct {
	Fill   HexColor `json:"fill"`
	Stroke HexColor `json:"stroke"`
}

type Radius struct {
	SM string `json:"sm"`
	MD string `json:"md"`
	LG string `json:"lg"`
}

type Shadows struct {
	SM string `json:"sm"`
	MD string `json:"md"`
}

type Fonts struct {
	Sans  string `json:"sans"`
	Serif string `json:"serif"`
	Mono  string `json:"mono"`
}
