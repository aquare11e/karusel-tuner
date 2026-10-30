package epg

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"
	"time"
)

const testSiteURL = "https://www.karusel-tv.ru/schedule/Europe/Moscow"

func renderXMLTV(t *testing.T, progs ...Programme) string {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteXMLTV(&buf, testSiteURL, progs); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestWriteXMLTVProgramme(t *testing.T) {
	out := renderXMLTV(t, Programme{
		Title:    "Спокойной ночи, малыши!",
		Announce: "Серия А — Серия Б",
		Link:     "/announce/1-snm",
		Start:    time.Unix(1793383200, 0),
		Stop:     time.Unix(1793384400, 0),
	})
	if !strings.HasPrefix(out, `<?xml version="1.0" encoding="UTF-8"?>`) {
		t.Errorf("missing XML declaration: %.60q", out)
	}
	for _, want := range []string{
		`<channel id="1">`,
		`<display-name>Карусель</display-name>`,
		`<display-name>1</display-name>`,
		`start="20261030210000 +0300"`,
		`stop="20261030212000 +0300"`,
		`channel="1"`,
		`<title lang="ru">Спокойной ночи, малыши!</title>`,
		`<sub-title lang="ru">30.10.2026</sub-title>`,
		`<desc lang="ru">Серия А — Серия Б</desc>`,
		`<url>https://www.karusel-tv.ru/announce/1-snm</url>`,
		`<episode-num system="xmltv_ns">2025.1029.</episode-num>`,
		`<episode-num system="onscreen">S2026E1030</episode-num>`,
		`<new>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %s\n%s", want, out)
		}
	}
}

func TestWriteXMLTVConvertsToMoscow(t *testing.T) {
	out := renderXMLTV(t, Programme{Title: "Смешарики", Start: time.Unix(1793325600, 0).UTC(), Stop: time.Unix(1793332800, 0).UTC()})
	if !strings.Contains(out, `start="20261030050000 +0300"`) {
		t.Errorf("start not in MSK:\n%s", out)
	}
}

func TestWriteXMLTVEscapes(t *testing.T) {
	title := "Том & Джерри <1>"
	out := renderXMLTV(t, Programme{Title: title, Start: time.Unix(1793383200, 0), Stop: time.Unix(1793384400, 0)})
	var doc struct {
		Programmes []struct {
			Title string `xml:"title"`
		} `xml:"programme"`
	}
	if err := xml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("invalid XML: %v\n%s", err, out)
	}
	if len(doc.Programmes) != 1 || doc.Programmes[0].Title != title {
		t.Fatalf("title round-trip = %+v", doc.Programmes)
	}
}

func TestWriteXMLTVOmitsEmptyOptional(t *testing.T) {
	out := renderXMLTV(t, Programme{Title: "Погода", Start: time.Unix(1793383200, 0), Stop: time.Unix(1793384400, 0)})
	if strings.Contains(out, "<desc") || strings.Contains(out, "<url") {
		t.Errorf("empty desc/url rendered:\n%s", out)
	}
}
