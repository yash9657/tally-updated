package form140

// THE RECORD LAYOUTS, transcribed from docs/statutory/form-140-file-format.xlsx
// sheet "File Format_R", one entry per "Sr. No." row of each record's table.
//
// ===========================================================================
// WHY THE LAYOUT IS DATA AND NOT CODE
// ===========================================================================
// General Note 10: "The total number of delimiters in any type of record should
// be one less than the total number of fields in the respective record."
//
// A serialiser that appended fields as it went would satisfy that note on the
// day it was written and break it the first time a branch skipped an optional
// field — producing a record with one fewer delimiter, which shifts every
// subsequent column by one and files a deductee's PAN into the name field. That
// failure is invisible in Go and is caught only by the FVU, which cannot run
// here.
//
// So: a record is a fixed, ordered table of fields; a value is SET BY FIELD
// NUMBER into a slice sized by the table; and serialisation joins the whole
// slice. The delimiter count is then fields-1 by construction, for every branch
// that will ever exist.
//
// ===========================================================================
// ⚠️ THE SPEC CONTRADICTS ITSELF ABOUT THE FILE HEADER, AND THE TABLE WINS
// ===========================================================================
// General Note 10 illustrates itself with: "the total number of fields in 'File
// Header record' is 16, hence the total no. of delimiters in 'File Header
// record' would be 15." THE FILE HEADER TABLE IN THE SAME WORKBOOK LISTS
// EIGHTEEN ROWS (Sr. Nos. 1-18, ending "Consolidated file hash").
//
// This package emits 18 fields / 17 delimiters, because the note is a worked
// example in prose and the table is the layout — and the two extra rows are
// trailing "Not applicable" hash fields, exactly what a later version of a
// format appends while its introductory prose goes stale. RECORDED, NOT
// SILENTLY RESOLVED: if the FVU rejects the header, this is the first thing to
// try, and TestFileHeaderHasEighteenFields is the line to change.
type kind uint8

const (
	// kindUnused is a field whose Remarks read "No value should be specified".
	// It is a distinct kind rather than an optional CHAR so that setting one is
	// a programming error rather than a silently-accepted extra value.
	kindUnused kind = iota
	kindChar
	kindInteger
	// kindAmount is a money field at 2 decimals: "1000.50".
	kindAmount
	// kindWholeAmount is a money field at 2 decimals whose paise MUST be zero:
	// "value 2345 will not be allowed, whereas value 2345.00 will be allowed",
	// and separately "No fractional portion is allowed in this field". Both
	// halves matter, and neither alone is the rule.
	kindWholeAmount
	// kindRate is the rate of deduction at 4 decimals: "if the rate is 2 then
	// the same should be mentioned as 2.0000" (General Note 5).
	kindRate
	// kindDate is ddmmyyyy, and may never be in the future (General Note 8).
	kindDate
)

// need is the workbook's M/O column, transcribed as printed.
type need uint8

const (
	needNA need = iota
	needOptional
	needMandatory
)

// field is one "Sr. No." row.
type field struct {
	no   int
	name string
	k    kind
	size int
	n    need
	// cond marks a field whose Remarks attach a condition to its M/O value —
	// "mandatory only when X", "no value should be provided if Y". The generic
	// presence check SKIPS these, because it cannot evaluate the condition, and
	// the rules in build.go own them instead. Without this flag the generic
	// check would have to be either wrong or absent; with it, every field is
	// covered by exactly one of the two.
	cond bool
}

type record struct {
	typ    string
	title  string
	fields []field
}

func f(no int, name string, k kind, size int, n need) field {
	return field{no: no, name: name, k: k, size: size, n: n}
}

func fc(no int, name string, k kind, size int, n need) field {
	return field{no: no, name: name, k: k, size: size, n: n, cond: true}
}

// na is a "Not applicable / No value should be specified" row.
func na(no int, name string) field {
	return field{no: no, name: name, k: kindUnused, n: needNA}
}

// fillers expands the workbook's runs of "Filler N" rows, which are numerous
// and identical. first is the Sr. No. of the first one; n how many; label the
// workbook's own name for it, because the numbering of "Filler" restarts per
// record and is not contiguous within one.
func fillers(first, n int, labels ...string) []field {
	out := make([]field, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, na(first+i, labels[i]))
	}
	return out
}

func join(groups ...[]field) []field {
	var out []field
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

// ---------------------------------------------------------------------------
// File Header — "TDS Statement for Resident - Non-Salary category (File Header
// Record)". One per file.
// ---------------------------------------------------------------------------

const (
	fhLineNumber = 1
	fhRecordType = 2
	fhFileType   = 3
	fhUploadType = 4
	fhCreated    = 5
	fhSequenceNo = 6
	fhUploader   = 7
	fhTAN        = 8
	fhBatchCount = 9
	fhRPUName    = 10
)

var fileHeader = record{
	typ:   "FH",
	title: "File Header",
	fields: join([]field{
		f(fhLineNumber, "Line Number", kindInteger, 9, needMandatory),
		f(fhRecordType, "Record Type", kindChar, 2, needMandatory),
		f(fhFileType, "File Type", kindChar, 4, needMandatory),
		f(fhUploadType, "Upload Type", kindChar, 2, needMandatory),
		f(fhCreated, "File Creation Date", kindDate, 8, needMandatory),
		f(fhSequenceNo, "File Sequence No.", kindInteger, 9, needMandatory),
		f(fhUploader, "Uploader Type", kindChar, 1, needMandatory),
		f(fhTAN, "TAN of Deductor", kindChar, 10, needMandatory),
		f(fhBatchCount, "Total No. of Batches", kindInteger, 9, needMandatory),
		f(fhRPUName, "Name of Return Preparation Utility", kindChar, 75, needMandatory),
	}, []field{
		na(11, "Record Hash (Not applicable)"),
		na(12, "FVU Version (Not applicable)"),
		na(13, "File Hash (Not applicable)"),
		na(14, "Sam Version (Not applicable)"),
		na(15, "SAM Hash (Not applicable)"),
		na(16, "SCM Version (Not applicable)"),
		na(17, "SCM Hash (Not applicable)"),
		na(18, "Consolidated file hash"),
	}),
}

// The File Header constants. All BLOCK CAPITALS, per General Note 3.
const (
	fileTypeNonSalary = "NS1" // "Value should be \"NS1\" for FN 140"
	uploadTypeRegular = "R"
	uploaderTypeD     = "D"
)

// ---------------------------------------------------------------------------
// Batch Header. "Single Batch Header record for the entire file" — the
// workbook's own note under the table, which is why totalBatches is always 1.
// ---------------------------------------------------------------------------

const (
	bhLineNumber      = 1
	bhRecordType      = 2
	bhBatchNumber     = 3
	bhChallanCount    = 4
	bhFormNumber      = 5
	bhPriorToken      = 9
	bhTAN             = 13
	bhPAN             = 15
	bhAssessmentYear  = 16
	bhTaxYear         = 17
	bhPeriod          = 18
	bhDeductorName    = 19
	bhAddrCountry     = 20
	bhAddr1           = 21
	bhAddr2           = 22
	bhAddr3           = 23
	bhAddr4           = 24
	bhAddr5           = 25
	bhState           = 26
	bhPincode         = 27
	bhEmail           = 28
	bhContactCC       = 29
	bhContact         = 30
	bhDeductorType    = 32
	bhRespName        = 33
	bhRespDesignation = 34
	bhRespAddr1       = 35
	bhRespAddr2       = 36
	bhRespAddr3       = 37
	bhRespAddr4       = 38
	bhRespAddr5       = 39
	bhRespState       = 40
	bhRespPin         = 41
	bhRespEmail       = 42
	bhRespCountry     = 43
	bhRespContactCC   = 44
	bhRespContact     = 45
	bhDepositTotal    = 47
	bhPriorFiled      = 52
	bhStateName       = 54
	bhMinistry        = 57
	bhMinistryOther   = 58
	bhRespPAN         = 59
	bhAIN             = 68
	bhGSTIN           = 69
)

var batchHeader = record{
	typ:   "BH",
	title: "Batch Header",
	fields: join([]field{
		f(bhLineNumber, "Line Number", kindInteger, 9, needMandatory),
		f(bhRecordType, "Record Type", kindChar, 2, needMandatory),
		f(bhBatchNumber, "Batch Number", kindInteger, 9, needMandatory),
		f(bhChallanCount, "Count of Challan/transfer voucher Records", kindInteger, 9, needMandatory),
		f(bhFormNumber, "Form Number", kindChar, 4, needMandatory),
		na(6, "Transaction Type (Not applicable)"),
		na(7, "Batch Updation Indicator (Not applicable)"),
		na(8, "Original Token Number (Not applicable)"),
		// Mandatory only when field 52 is "Y". Conditional, not optional.
		fc(bhPriorToken, "Token no. of previous regular statement (Form no. 140)", kindInteger, 15, needOptional),
		na(10, "Token Number of the statement submitted (Not applicable)"),
		na(11, "Token Number date (Not applicable)"),
		na(12, "Last TAN of Deductor / Collector (Not applicable)"),
		f(bhTAN, "TAN of Deductor", kindChar, 10, needMandatory),
		na(14, "Receipt number (eight digit) provided by TIN"),
		f(bhPAN, "PAN of Deductor", kindChar, 10, needMandatory),
		f(bhAssessmentYear, "Assessment Yr", kindInteger, 6, needMandatory),
		f(bhTaxYear, "Tax Year", kindInteger, 6, needMandatory),
		f(bhPeriod, "Period", kindChar, 2, needMandatory),
		f(bhDeductorName, "Name of Deductor", kindChar, 75, needMandatory),
		f(bhAddrCountry, "Deductor Address 6 (Country/Region)", kindChar, 25, needMandatory),
		f(bhAddr1, "Deductor Address1 (Flat/Door/Block number)", kindChar, 25, needMandatory),
		f(bhAddr2, "Deductor Address2 (Post Office)", kindChar, 25, needOptional),
		f(bhAddr3, "Deductor Address3 (Road/Street/Block/Sector)", kindChar, 25, needOptional),
		f(bhAddr4, "Deductor Address4 (Area/locality)", kindChar, 25, needOptional),
		f(bhAddr5, "Deductor Address5 (District)", kindChar, 25, needOptional),
		f(bhState, "Deductor's Address - State", kindInteger, 2, needMandatory),
		f(bhPincode, "Deductor's Address - Pincode", kindInteger, 6, needMandatory),
		// M in the workbook, but: "This field is mandatory if no value is
		// provided under field no. 42". The pair rule is in build.go.
		fc(bhEmail, "Deductor's Email ID", kindChar, 75, needMandatory),
		f(bhContactCC, "Deductor Contact Country Code", kindInteger, 5, needMandatory),
		f(bhContact, "Deductor Contact Number", kindChar, 10, needMandatory),
		na(31, "Filler 1"),
		f(bhDeductorType, "Deductor Type", kindChar, 1, needMandatory),
		f(bhRespName, "Name of Person responsible for Deduction", kindChar, 75, needMandatory),
		f(bhRespDesignation, "Designation of the Person responsible for Deduction", kindChar, 20, needMandatory),
		f(bhRespAddr1, "Responsible Person's Address1 (Flat/Door/Block number)", kindChar, 25, needMandatory),
		f(bhRespAddr2, "Responsible Person's Address2 (Post Office)", kindChar, 25, needOptional),
		f(bhRespAddr3, "Responsible Person's Address3 (Road/Street/Block/Sector)", kindChar, 25, needOptional),
		f(bhRespAddr4, "Responsible Person's Address4 (Area/locality)", kindChar, 25, needOptional),
		f(bhRespAddr5, "Responsible Person's Address5 (District)", kindChar, 25, needOptional),
		f(bhRespState, "Responsible Person's State", kindInteger, 2, needMandatory),
		f(bhRespPin, "Responsible Person's PIN", kindInteger, 6, needMandatory),
		// The other half of the email pair rule.
		fc(bhRespEmail, "Responsible Person's Email ID -1", kindChar, 75, needMandatory),
		f(bhRespCountry, "Responsible Persons country region", kindChar, 25, needMandatory),
		f(bhRespContactCC, "Responsible Person Contact Country/Region", kindInteger, 5, needMandatory),
		f(bhRespContact, "Responsible Person Contact Number", kindChar, 10, needMandatory),
		na(46, "Filler 3"),
		f(bhDepositTotal, "Batch Total of - Total of Deposit Amount as per Challan", kindWholeAmount, 15, needMandatory),
		na(48, "Unmatched challan count"),
		na(49, "Count of Salary Details Records (Not applicable)"),
		na(50, "Batch Total of - Gross Total Income as per Salary Detail (Not applicable)"),
		na(51, "Filler 4"),
		f(bhPriorFiled, "Whether regular statement for Form 140 filed for earlier period", kindChar, 1, needMandatory),
		na(53, "Last Deductor Type"),
		// Mandatory for the four State-Government deductor types, forbidden
		// for every other — so conditional in BOTH directions.
		fc(bhStateName, "State Name", kindChar, 2, needOptional),
		na(55, "Filler 5"),
		na(56, "Filler 6"),
		fc(bhMinistry, "Ministry Name", kindChar, 3, needOptional),
		fc(bhMinistryOther, "Ministry Name (Others)", kindChar, 150, needOptional),
		f(bhRespPAN, "PAN of Responsible Person", kindChar, 10, needMandatory),
	},
		fillers(60, 8, "Filler 7", "Filler 8", "Filler 9", "Filler 10",
			"Filler 11", "Filler 12", "Filler 13", "Filler 14"),
		[]field{
			fc(bhAIN, "Account Office Identification Number (AIN) of PAO/TO/CDDO", kindChar, 7, needOptional),
			f(bhGSTIN, "Goods and Service Tax Number (GSTN)", kindChar, 15, needOptional),
			na(70, "Count of Section 194P Detail Records (Not applicable)"),
			na(71, "Batch Total of - Gross Total Income as per Section 194P Detail (Not applicable)"),
			na(72, "Record Hash (Not applicable)"),
		}),
}

// ---------------------------------------------------------------------------
// Challan Detail. "Separate records for each Challan."
// ---------------------------------------------------------------------------

const (
	cdLineNumber    = 1
	cdRecordType    = 2
	cdBatchNumber   = 3
	cdRecordNumber  = 4
	cdDeducteeCount = 5
	cdNilIndicator  = 6
	cdTaxDeducted   = 8
	cdInterest      = 9
	cdFee           = 10
	cdPenalty       = 11
	cdDepositTotal  = 12
	cdMode          = 13
	cdBSRCode       = 15
	cdChallanNo     = 17
	cdDepositDate   = 19
	cdAnnexDeposit  = 21
	cdAnnexDeducted = 22
	cdMinorHead     = 23
)

var challanDetail = record{
	typ:   "CD",
	title: "Challan Detail",
	fields: join([]field{
		f(cdLineNumber, "Line Number", kindInteger, 9, needMandatory),
		f(cdRecordType, "Record Type", kindChar, 2, needMandatory),
		f(cdBatchNumber, "Batch Number", kindInteger, 9, needMandatory),
		f(cdRecordNumber, "Challan-Detail Record Number", kindInteger, 9, needMandatory),
		f(cdDeducteeCount, "Count of Deductee / Party Records", kindInteger, 9, needMandatory),
		f(cdNilIndicator, "NIL Challan Indicator", kindChar, 1, needMandatory),
		na(7, "Challan Updation Indicator (Not applicable)"),
		f(cdTaxDeducted, "Total tax Deducted", kindWholeAmount, 15, needMandatory),
		f(cdInterest, "Total Interest", kindWholeAmount, 15, needMandatory),
		f(cdFee, "Total fee", kindWholeAmount, 15, needMandatory),
		f(cdPenalty, "Total Penalty/ Others", kindWholeAmount, 15, needMandatory),
		f(cdDepositTotal, "Total of Deposit Amount as per Challan", kindWholeAmount, 15, needMandatory),
		fc(cdMode, "Mode of payment of tax", kindChar, 1, needOptional),
		na(14, "Last BSR Code/ Form 24G Receipt Number (Not applicable)"),
		fc(cdBSRCode, "BSR Code/ Form 24G Receipt Number", kindInteger, 7, needMandatory),
		na(16, "Last Bank Challan No/DDO Serial Number (Not applicable)"),
		fc(cdChallanNo, "Bank Challan No/DDO Serial Number", kindInteger, 5, needOptional),
		na(18, "Last Date of 'Bank Challan No / DDO Serial Number' (Not applicable)"),
		fc(cdDepositDate, "Date of 'Bank Challan / Transfer Voucher'", kindDate, 8, needMandatory),
		na(20, "Last Total of Deposit Amount as per Challan (Not applicable)"),
		f(cdAnnexDeposit, "Total Tax Deposited Amount as per Collectee annexure", kindAmount, 15, needMandatory),
		f(cdAnnexDeducted, "Total Tax Deducted Amount as per Deductor annexure", kindAmount, 15, needMandatory),
		fc(cdMinorHead, "Minor Head of Challan", kindInteger, 3, needOptional),
	},
		fillers(24, 6, "Filler 1", "Filler 2", "Filler 3", "Filler 4", "Filler 5", "Filler 6"),
		[]field{na(30, "Record Hash (Not applicable)")}),
}

// The Challan Detail constants.
const (
	nilChallanYes = "Y"
	nilChallanNo  = "N"
	// "Allowed values C or B. If Transfer Voucher Number is provided this is
	// mandatory and only allowed value is 'B'. If Bank Challan Number is
	// provided, then mention value as 'C'."
	modeChallan     = "C"
	modeBookAdjust  = "B"
	minorHeadNormal = "200"
)

// ---------------------------------------------------------------------------
// Deductee Detail. "Separate records for each Collectee" (the workbook's own
// wording, carried over from the collection-side form).
// ---------------------------------------------------------------------------

const (
	ddLineNumber   = 1
	ddRecordType   = 2
	ddBatchNumber  = 3
	ddRecordNumber = 4
	ddChallanRef   = 5
	ddMode         = 6
	ddPAN          = 8
	ddName         = 9
	ddSectionCode  = 15
	ddDeposited    = 18
	ddPaymentDate  = 19
	ddAmountPaid   = 20
	ddCashOver1Cr  = 22
	ddCashOver3Cr  = 23
	ddTaxDeducted  = 24
	ddTaxDeposited = 25
	ddDeductDate   = 27
	ddRate         = 28
	ddUINForm121   = 31
	ddReasonFlag   = 32
	ddCertificate  = 33
)

var deducteeDetail = record{
	typ:   "DD",
	title: "Deductee Detail",
	fields: join([]field{
		f(ddLineNumber, "Line Number", kindInteger, 9, needMandatory),
		f(ddRecordType, "Record Type", kindChar, 2, needMandatory),
		f(ddBatchNumber, "Batch Number", kindInteger, 9, needMandatory),
		f(ddRecordNumber, "Deductee / Party Detail Record No", kindInteger, 9, needMandatory),
		f(ddChallanRef, "Challan reference Number", kindInteger, 9, needMandatory),
		f(ddMode, "Mode", kindChar, 1, needMandatory),
		na(7, "Last deductee PAN (Not applicable)"),
		f(ddPAN, "Deductee's PAN", kindChar, 10, needMandatory),
		f(ddName, "Name of the Deductee", kindChar, 75, needMandatory),
	},
		fillers(10, 5, "Remarks 1 (for future use)", "Remarks 2 (for future use)",
			"Remarks 3 (for future use)", "Remarks 4 (for future use)",
			"Remarks 5 (for future use)"),
		[]field{
			f(ddSectionCode, "Section Code", kindChar, 4, needMandatory),
			na(16, "Remarks 6 (for future use)"),
			na(17, "Remarks 7 (for future use)"),
			f(ddDeposited, "Whether the total amount of tax deducted has been deposited", kindChar, 1, needMandatory),
			f(ddPaymentDate, "Date of payment / Credited", kindDate, 8, needMandatory),
			f(ddAmountPaid, "Amount Paid/ Credited", kindAmount, 15, needMandatory),
			na(21, "Grossing up Indicator (Not applicable)"),
			fc(ddCashOver1Cr, "Amount of cash withdrawal in excess of Rs. 1 crore (s.393(3) Sl. No. 5)", kindAmount, 15, needOptional),
			fc(ddCashOver3Cr, "Amount of cash withdrawal in excess of Rs. 3 crore (co-operative societies)", kindAmount, 15, needOptional),
			f(ddTaxDeducted, "Total Tax Deducted", kindAmount, 15, needMandatory),
			f(ddTaxDeposited, "Total Tax Deposited", kindAmount, 15, needMandatory),
			na(26, "Last total Tax deducted (Not applicable)"),
			// "Mandatory if 'Total tax deducted' is greater than Zero. No value
			// needs to be specified if 'Total Tax Deducted' is Zero."
			fc(ddDeductDate, "Date of deduction", kindDate, 8, needMandatory),
			f(ddRate, "Rate at which Tax Deducted", kindRate, 7, needMandatory),
			na(29, "Remarks 9 (for future use)"),
			na(30, "Remarks 10 (for future use)"),
			f(ddUINForm121, "Unique Identification Number of Form No. 121 submitted by the deductee", kindChar, 26, needOptional),
			f(ddReasonFlag, "Reason for non-deduction / lower deduction / higher deduction / threshold / transporter", kindChar, 1, needOptional),
			// "Mandatory to mention 15 digit alphanumeric value if 'A' is
			// mentioned in field no. 32."
			fc(ddCertificate, "Certificate number of certificate issued under section 395", kindChar, 15, needOptional),
		},
		fillers(34, 8, "Filler 1", "Filler 2", "Filler 3", "Filler 4",
			"Filler 5", "Filler 6", "Filler 7", "Filler 8"),
		[]field{
			na(42, "Last Total Tax Deposited (Not applicable)"),
			na(43, "Filler 10"),
			na(44, "Filler 11"),
			na(45, "Record Hash (Not applicable)"),
		}),
}

// ddModeO is DD field 6: "Value should be O".
const ddModeO = "O"

// allRecords is every layout, for the tests that assert the tables are
// self-consistent (Sr. Nos. contiguous from 1, no duplicate field numbers).
var allRecords = []*record{&fileHeader, &batchHeader, &challanDetail, &deducteeDetail}
