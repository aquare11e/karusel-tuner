package epg

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
)

// Channel identity shared by the XMLTV guide and the HDHomeRun lineup, so
// Plex can match them without manual mapping.
const (
	ChannelID   = "1"
	ChannelName = "Карусель"
)

const xmltvTime = "20060102150405 -0700"

type xmlTV struct {
	XMLName    xml.Name       `xml:"tv"`
	Generator  string         `xml:"generator-info-name,attr"`
	Channel    xmlChannel     `xml:"channel"`
	Programmes []xmlProgramme `xml:"programme"`
}

type xmlChannel struct {
	ID           string   `xml:"id,attr"`
	DisplayNames []string `xml:"display-name"`
}

type xmlText struct {
	Lang  string `xml:"lang,attr"`
	Value string `xml:",chardata"`
}

type xmlEpisode struct {
	System string `xml:"system,attr"`
	Value  string `xml:",chardata"`
}

// Element order follows the XMLTV DTD.
type xmlProgramme struct {
	Start    string       `xml:"start,attr"`
	Stop     string       `xml:"stop,attr"`
	Channel  string       `xml:"channel,attr"`
	Title    xmlText      `xml:"title"`
	SubTitle xmlText      `xml:"sub-title"`
	Desc     *xmlText     `xml:"desc,omitempty"`
	URL      string       `xml:"url,omitempty"`
	Episodes []xmlEpisode `xml:"episode-num"`
	New      *struct{}    `xml:"new"`
}

// WriteXMLTV renders the single-channel guide. Each airing is an episode
// keyed by its Moscow air date, so Plex series recordings treat every daily
// broadcast as new and name files by date.
func WriteXMLTV(w io.Writer, siteURL string, progs []Programme) error {
	base, _ := url.Parse(siteURL)
	doc := xmlTV{
		Generator: "karusel-plex-relay",
		Channel:   xmlChannel{ID: ChannelID, DisplayNames: []string{ChannelName, ChannelID}},
	}
	for _, p := range progs {
		start := p.Start.In(Moscow)
		year, month, day := start.Date()
		mmdd := int(month)*100 + day
		xp := xmlProgramme{
			Start:    start.Format(xmltvTime),
			Stop:     p.Stop.In(Moscow).Format(xmltvTime),
			Channel:  ChannelID,
			Title:    xmlText{Lang: "ru", Value: p.Title},
			SubTitle: xmlText{Lang: "ru", Value: start.Format("02.01.2006")},
			URL:      absoluteLink(base, p.Link),
			Episodes: []xmlEpisode{
				{System: "xmltv_ns", Value: fmt.Sprintf("%d.%d.", year-1, mmdd-1)},
				{System: "onscreen", Value: fmt.Sprintf("S%dE%d", year, mmdd)},
			},
			New: &struct{}{},
		}
		if p.Announce != "" {
			xp.Desc = &xmlText{Lang: "ru", Value: p.Announce}
		}
		doc.Programmes = append(doc.Programmes, xp)
	}
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}

func absoluteLink(base *url.URL, link string) string {
	if base == nil || link == "" {
		return ""
	}
	ref, err := url.Parse(link)
	if err != nil {
		return ""
	}
	return base.ResolveReference(ref).String()
}
