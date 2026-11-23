package transport

import (
	"bytes"
	"encoding/csv"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"hi-ve/backend/internal/inventory"
	"hi-ve/backend/internal/platform/entitlements"
	"hi-ve/backend/internal/reports"
	"hi-ve/backend/internal/sales"
)

func (a API) reportsRoutes(mux *http.ServeMux) {
	if a.Reports.DB == nil {
		return
	}
	const root = "/api/v1/app/tenants/{tenant_id}/reports"
	mux.HandleFunc("GET "+root+"/capabilities", a.origin(a.AppOrigin, func(w http.ResponseWriter, r *http.Request) {
		caps, err := a.Reports.Capabilities(r.Context(), r.PathValue("tenant_id"), cookie(r, tenantCookie))
		if err != nil {
			reportError(w, err)
			return
		}
		respond(w, 200, caps)
	}))
	mux.HandleFunc("GET "+root+"/sales/{id}", a.origin(a.AppOrigin, func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Query()) != 1 || len(r.URL.Query()["location"]) != 1 {
			reportError(w, reports.ErrInput)
			return
		}
		out, err := a.Reports.Detail(r.Context(), r.PathValue("tenant_id"), cookie(r, tenantCookie), r.URL.Query().Get("location"), r.PathValue("id"))
		if err != nil {
			reportError(w, err)
			return
		}
		respond(w, 200, out)
	}))
	mux.HandleFunc("GET "+root+"/{kind}", a.origin(a.AppOrigin, func(w http.ResponseWriter, r *http.Request) {
		dataset := r.PathValue("kind")
		f, format, err := reportFilter(dataset, r)
		if err != nil {
			reportError(w, err)
			return
		}
		out, err := a.Reports.Read(r.Context(), cookie(r, tenantCookie), dataset, f)
		if err != nil {
			reportError(w, err)
			return
		}
		if format == "csv" {
			writeReportCSV(w, out)
			return
		}
		respond(w, 200, out)
	}))
}

func reportFilter(kind string, r *http.Request) (reports.Filter, string, error) {
	f := reports.Filter{Tenant: r.PathValue("tenant_id")}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return f, "", reports.ErrInput
	}
	for k, v := range query {
		if len(v) != 1 {
			return f, "", reports.ErrInput
		}
		switch k {
		case "from", "to", "date_from", "date_through", "timezone", "location", "cursor", "format":
		case "sku":
			if kind != "inventory" && kind != "movements" {
				return f, "", reports.ErrInput
			}
		case "actor", "movement":
			if kind != "movements" {
				return f, "", reports.ErrInput
			}
		default:
			return f, "", reports.ErrInput
		}
	}
	f.Location, f.SKU, f.Actor, f.Movement, f.Cursor = query.Get("location"), query.Get("sku"), query.Get("actor"), query.Get("movement"), query.Get("cursor")
	now := time.Now().UTC()
	f.To = time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
	f.From = f.To.Add(-24 * time.Hour)
	if query.Has("from") || query.Has("to") {
		f.From, err = time.Parse(time.RFC3339, query.Get("from"))
		if err != nil {
			return f, "", reports.ErrInput
		}
		f.To, err = time.Parse(time.RFC3339, query.Get("to"))
		if err != nil {
			return f, "", reports.ErrInput
		}
	}
	if query.Has("date_from") || query.Has("date_through") || query.Has("timezone") {
		zone := query.Get("timezone")
		if query.Has("from") || query.Has("to") || zone == "" || zone == "Local" || len(zone) > 64 {
			return f, "", reports.ErrInput
		}
		loc, err := time.LoadLocation(zone)
		if err != nil {
			return f, "", reports.ErrInput
		}
		from, err := time.Parse(time.DateOnly, query.Get("date_from"))
		if err != nil {
			return f, "", reports.ErrInput
		}
		through, err := time.Parse(time.DateOnly, query.Get("date_through"))
		if err != nil || through.Before(from) || through.Sub(from) >= reports.MaxReportDays*24*time.Hour {
			return f, "", reports.ErrInput
		}
		f.CalendarDays = int(through.Sub(from)/(24*time.Hour)) + 1
		start, startOK := civilDayStart(from, loc)
		_, throughOK := civilDayStart(through, loc)
		end, endOK := civilDayStart(through.AddDate(0, 0, 1), loc)
		// A full civil date can be skipped when a zone changes sides of the date line.
		if !endOK {
			end, endOK = civilDayStart(through.AddDate(0, 0, 2), loc)
		}
		if !startOK || !throughOK || !endOK {
			return f, "", reports.ErrInput
		}
		f.From, f.To = start, end
	}
	if kind == "inventory" {
		f.From, f.To = time.Time{}, time.Time{}
	}
	format := query.Get("format")
	if format != "" && format != "csv" || format == "csv" && kind == "dashboard" {
		return f, "", reports.ErrInput
	}
	return f, format, nil
}

// Find the first valid instant in the requested calendar day. Each zone
// interval supplies its own offset, including shifts that skip local midnight.
func civilDayStart(date time.Time, loc *time.Location) (time.Time, bool) {
	for instant, limit := date.Add(-48*time.Hour), date.Add(48*time.Hour); instant.Before(limit); {
		local := instant.In(loc)
		_, offset := local.Zone()
		_, next := local.ZoneBounds()
		start := date.Add(-time.Duration(offset) * time.Second)
		if instant.After(start) {
			start = instant
		}
		if (next.IsZero() || start.Before(next)) && start.In(loc).Format(time.DateOnly) == date.Format(time.DateOnly) {
			return start.UTC(), true
		}
		if next.IsZero() {
			break
		}
		instant = next
	}
	return time.Time{}, false
}

func reportError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, reports.ErrInput):
		fail(w, 400, "INVALID_REPORT_FILTER")
	case errors.Is(err, entitlements.ErrDenied):
		fail(w, 403, "ENTITLEMENT_DENIED")
	case errors.Is(err, sales.ErrUnavailable):
		fail(w, 404, "NOT_FOUND")
	default:
		handleError(w, err)
	}
}

var negativeDecimal = regexp.MustCompile(`^-[0-9]+(?:[.][0-9]+)?$`)

func writeReportCSV(w http.ResponseWriter, out any) {
	var buffer bytes.Buffer
	c := csv.NewWriter(&buffer)
	write := func(fields ...string) {
		for i, value := range fields {
			if negativeDecimal.MatchString(value) {
				continue
			}
			trimmed := strings.TrimLeft(value, " ")
			if trimmed != "" && strings.ContainsRune("=+-@\t\r\n", rune(trimmed[0])) {
				fields[i] = "'" + value
			}
		}
		_ = c.Write(fields)
	}
	var next string
	switch p := out.(type) {
	case reports.Page[sales.ReportSale]:
		next = p.Cursor
		write("id", "outlet_id", "created_at", "method", "payment_state", "gross", "discount", "returns", "voids", "net", "received", "pending", "refund_recorded")
		for _, r := range p.Rows {
			write(r.ID, r.Outlet, r.At.Format(time.RFC3339Nano), r.Method, r.Payment, r.Gross, r.Discount, r.Returns, r.Voids, r.Net, r.Received, r.Pending, r.RefundPaid)
		}
	case reports.Page[inventory.ReportBalance]:
		next = p.Cursor
		write("location_id", "sku_id", "batch_id", "bucket", "quantity", "lot")
		for _, r := range p.Rows {
			write(r.Location, r.SKU, r.Batch, r.Bucket, r.Quantity, r.Lot)
		}
	case reports.Page[inventory.ReportMovement]:
		next = p.Cursor
		write("posted_at", "kind", "document_id", "line", "location_id", "sku_id", "batch_id", "bucket", "quantity_delta", "actor_id", "lot", "source_batch_id")
		for _, r := range p.Rows {
			write(r.At.Format(time.RFC3339Nano), r.Kind, r.Document, strconv.Itoa(r.Line), r.Location, r.SKU, r.Batch, r.Bucket, r.Delta, r.Actor, r.Lot, r.SourceBatch)
		}
	case reports.Page[inventory.ReportTransfer]:
		next = p.Cursor
		write("id", "source_location_id", "destination_location_id", "dispatched", "received", "transit", "discrepancy")
		for _, r := range p.Rows {
			write(r.ID, r.From, r.To, r.Dispatched, r.Received, r.Transit, r.Discrepancy)
		}
	default:
		fail(w, 400, "INVALID_REPORT_FILTER")
		return
	}
	c.Flush()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="hi-ve-report.csv"`)
	w.Header().Set("Access-Control-Expose-Headers", "X-Next-Cursor")
	if next != "" {
		w.Header().Set("X-Next-Cursor", next)
	}
	_, _ = w.Write(buffer.Bytes())
}
