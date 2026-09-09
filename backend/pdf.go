package main

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/jung-kurt/gofpdf"
)

// ---------------------------------------------------------------------------
// Human-readable certificate PDF. Rendered with the (archived but stable)
// github.com/jung-kurt/gofpdf v1.16.2 using only core fonts, so no font
// assets are shipped. Text is sanitized to Latin-1 — core fonts cannot
// represent other codepoints and would otherwise throw.
// ---------------------------------------------------------------------------

// pdfL1 maps a UTF-8 string to Latin-1 renderable bytes, replacing any
// codepoint outside 0x20..0xFF (and stray control chars) with '?'. gofpdf's
// core fonts write raw Latin-1 bytes into the content stream, so anything
// beyond 0xFF would corrupt the PDF or raise an error.
func pdfL1(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t' || r == '\r':
			b.WriteRune(' ')
		case r >= 0x20 && r <= 0xFF:
			b.WriteRune(r)
		default:
			b.WriteRune('?')
		}
	}
	return b.String()
}

func pdfSectionTitle(pdf *gofpdf.Fpdf, title string) {
	pdf.Ln(2)
	pdf.SetFont("Helvetica", "B", 11)
	pdf.SetFillColor(216, 224, 244)
	pdf.CellFormat(0, 7, pdfL1(title), "", 1, "L", true, 0, "")
	pdf.Ln(1)
}

func pdfKV(pdf *gofpdf.Fpdf, key, value string) {
	pdf.SetFont("Helvetica", "B", 9)
	pdf.Cell(55, 5, pdfL1(key))
	pdf.SetFont("Helvetica", "", 9)
	pdf.Cell(0, 5, pdfL1(value))
	pdf.Ln(5)
}

func pdfPageBreak(pdf *gofpdf.Fpdf) {
	if pdf.GetY() > 250 {
		pdf.AddPage()
		pdf.Ln(3)
	}
}

// renderCertificatePDF renders the certificate to a PDF and returns the file
// bytes. Rows (approvals, chain versions) that overflow a page continue on a
// fresh page.
func renderCertificatePDF(cert *chainOfCustodyCertificate) ([]byte, error) {
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(15, 15, 15)
	pdf.SetAutoPageBreak(true, 15)
	pdf.SetTitle(fmt.Sprintf("Chain of Custody — FIR %s", cert.Document.FirNumber), true)
	pdf.SetAuthor("Q-DESK", true)
	pdf.SetCreator("Q-DESK backend", true)
	pdf.AddPage()

	// Header.
	pdf.Ln(4)
	pdf.SetFont("Helvetica", "B", 16)
	pdf.CellFormat(0, 9, "CHAIN OF CUSTODY CERTIFICATE", "", 1, "C", false, 0, "")
	pdf.SetFont("Helvetica", "", 9)
	pdf.CellFormat(0, 6, "Q-DESK signed evidence export", "", 1, "C", false, 0, "")

	pdfKV(pdf, "Certificate ID", cert.CertificateID)
	pdfKV(pdf, "Generated At", cert.GeneratedAt)
	pdfKV(pdf, "Schema Version", cert.SchemaVersion)
	pdfKV(pdf, "Chain Integrity", strings.ToUpper(cert.ChainValid))
	pdfKV(pdf, "Server Public Key", shortKey(cert.ServerPublicKeyB64))

	pdfSectionTitle(pdf, "Document")
	pdfKV(pdf, "FIR Number", cert.Document.FirNumber)
	pdfKV(pdf, "Title", cert.Document.Title)
	pdfKV(pdf, "Classification", cert.Document.Classification)
	pdfKV(pdf, "Document ID", cert.Document.DocumentID)
	pdf.Ln(1)
	pdfKV(pdf, "Download Request ID", cert.DownloadRequestID)
	pdfKV(pdf, "Requested By", cert.RequestedByBadge+" "+cert.RequestedByName)
	pdfKV(pdf, "Reason", cert.Reason)
	pdfKV(pdf, "Requested At", cert.RequestedAt)
	pdfKV(pdf, "Approval Deadline", cert.ExpiresAt)
	pdfKV(pdf, "Downloaded At", cert.DownloadedAt)
	pdfKV(pdf, "Required Approvals", fmt.Sprintf("%d", cert.RequiredApprovals))

	pdfSectionTitle(pdf, "Witness Approvals")
	if len(cert.Approvals) == 0 {
		pdf.SetFont("Helvetica", "I", 9)
		pdf.Cell(0, 5, "None recorded.")
		pdf.Ln(5)
	}
	for _, ap := range cert.Approvals {
		pdfPageBreak(pdf)
		pdf.SetFont("Helvetica", "B", 9)
		pdf.Cell(60, 5, pdfL1(ap.ApprovedByBadge+" "+ap.ApproverName))
		pdf.SetFont("Helvetica", "", 9)
		switch ap.Decision {
		case "approved":
			pdf.SetTextColor(0, 128, 0)
		case witnessRejected:
			pdf.SetTextColor(190, 0, 0)
		}
		pdf.Cell(25, 5, strings.ToUpper(pdfL1(ap.Decision)))
		pdf.SetTextColor(0, 0, 0)
		pdf.Cell(55, 5, pdfL1(ap.DecidedAt))
		pdf.Cell(0, 5, "sig "+shortKey(ap.Signature))
		pdf.Ln(5)
	}
	pdf.SetTextColor(0, 0, 0)

	pdfSectionTitle(pdf, "Version Chain")
	cols := []float64{12, 20, 12, 22, 34, 20, 16, 18, 26}
	heads := []string{"V", "Path", "Kind", "Badge", "Created", "Type", "Verify", "", "Signature"}
	pdf.SetFont("Helvetica", "B", 8)
	pdf.SetFillColor(230, 232, 240)
	for i, h := range heads {
		pdf.CellFormat(cols[i], 6, pdfL1(h), "1", 0, "C", true, 0, "")
	}
	pdf.Ln(6)
	pdf.SetFont("Helvetica", "", 7.5)
	for _, v := range cert.Chain {
		pdfPageBreak(pdf)
		verify := strings.ToUpper(v.ChainValid)
		if verify != "VALID" {
			pdf.SetTextColor(190, 0, 0)
		}
		pdf.Cell(cols[0], 5, fmt.Sprintf("%d", v.Version))
		pdf.Cell(cols[1], 5, pdfL1(v.TreePath))
		pdf.Cell(cols[2], 5, pdfL1(v.Kind))
		pdf.Cell(cols[3], 5, pdfL1(shortKey(v.Badge)))
		pdf.Cell(cols[4], 5, pdfL1(v.CreatedAt))
		pdf.Cell(cols[5], 5, pdfL1(shortType(v.ContentType)))
		pdf.Cell(cols[6], 5, verify)
		pdf.Cell(cols[7], 5, "")
		pdf.Cell(cols[8], 5, "sig "+shortKey(v.Signature))
		pdf.Ln(5)
		pdf.SetTextColor(0, 0, 0)
	}

	pdf.Ln(4)
	pdf.SetFont("Helvetica", "I", 8)
	pdf.CellFormat(0, 5, "Signed with the Q-DESK server ML-DSA-65 key. Verify the server_signature over", "", 1, "L", false, 0, "")
	pdf.CellFormat(0, 5, "the JSON manifest with server_signature empty; approved_at fields recompute the", "", 1, "L", false, 0, "")
	pdf.CellFormat(0, 5, "witness signatures from request_id|decision|decided_at.", "", 1, "L", false, 0, "")

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// shortKey renders a base64 key/signature preview (~24 chars) for the PDF.
func shortKey(s string) string {
	if len(s) <= 24 {
		return s
	}
	return s[:24] + "..."
}

// shortType truncates a mime type for the narrow chain table column.
func shortType(s string) string {
	if len(s) <= 20 {
		return s
	}
	return s[:20]
}
