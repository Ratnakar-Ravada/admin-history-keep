// emailpreview renders a fictional card locally. It never connects to Gmail.
package main

import (
	"fmt"
	"locationkeeper/server/internal/app"
	"log"
)

func main() {
	_, html, err := app.RenderAlertCard(app.AlertCard{Title: "Example: missed check-ins", Message: "This is a fictional email preview. Automated alert monitoring is coming soon.", Device: "Example phone", Area: "Example city area", When: "18 November 2026, 09:30 IST", AppURL: "https://admin.location-keep.rockyworks.in/?view=logs"})
	if err != nil {
		log.Fatal("Unable to render preview")
	}
	fmt.Print(html)
}
