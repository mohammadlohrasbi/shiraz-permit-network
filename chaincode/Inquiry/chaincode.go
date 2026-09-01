package main

// ---------------------------------------------------------------------------
// Inquiry — استعلام بین‌دستگاهی
//
// ── چرا این قرارداد بیشترین اثر را بر «تسهیل فرآیند» دارد ────────────────
//
// در پرونده پروانه، بیشترین زمان تلف‌شده نه در محاسبه است نه در بررسی نقشه؛
// در انتظار پاسخ استعلام است. متقاضی با نامه کاغذی بین اداره ثبت، آب، برق،
// گاز، مخابرات، آتش‌نشانی، محیط زیست و میراث فرهنگی می‌گردد، و شهرداری هیچ
// دید متمرکزی ندارد که کدام استعلام کجا گیر کرده است.
//
// دو چیز اینجا آن را عوض می‌کند:
//
// ۱) استعلام یک درخواست ثبت‌شده با مهلت مشخص است، نه یک نامه. لحظه ارسال،
//    مهلت، و لحظه پاسخ روی زنجیره‌اند. «کدام دستگاه دیر می‌کند» از یک حدس
//    به یک گزارش تبدیل می‌شود.
//
// ۲) تأیید ضمنی (deemed approval): اگر دستگاهی تا پایان مهلت پاسخ ندهد،
//    استعلام مثبت تلقی می‌شود. این تنها ابزاری است که سکوت را پرهزینه
//    می‌کند. بدون آن، هر مهلتی روی کاغذ می‌ماند.
//
// ⚠️ تأیید ضمنی برای همه استعلام‌ها مجاز نیست. استعلام‌هایی که ماهیت ایمنی
// یا حفاظتی دارند (میراث فرهنگی، آتش‌نشانی، محیط زیست) از آن مستثنا هستند:
// سکوت اداره میراث را نمی‌شود «مانعی ندارد» خواند وقتی پای حریم یک اثر
// ثبت‌شده در میان است. برای آنها سکوت فقط هشدار تولید می‌کند و پرونده
// می‌ماند.
// ---------------------------------------------------------------------------

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/hyperledger/fabric-contract-api-go/contractapi"
)

type InquiryContract struct {
	NetworkBase
}

// دستگاه‌های استعلام‌شونده
const (
	AgencyRegistry = "REGISTRY" // اداره ثبت اسناد و املاک
	AgencyWater    = "WATER"    // آب و فاضلاب
	AgencyPower    = "POWER"    // توزیع برق
	AgencyGas      = "GAS"      // گاز
	AgencyTelecom  = "TELECOM"  // مخابرات
	AgencyFire     = "FIRE"     // آتش‌نشانی و خدمات ایمنی
	AgencyEnv      = "ENV"      // محیط زیست
	AgencyHeritage = "HERITAGE" // میراث فرهنگی
	AgencyRoads    = "ROADS"    // راه و شهرسازی
	AgencyJustice  = "JUSTICE"  // مراجع قضایی
)

var agencyTitle = map[string]string{
	AgencyRegistry: "اداره ثبت اسناد و املاک",
	AgencyWater:    "شرکت آب و فاضلاب",
	AgencyPower:    "شرکت توزیع نیروی برق",
	AgencyGas:      "شرکت گاز",
	AgencyTelecom:  "مخابرات",
	AgencyFire:     "سازمان آتش‌نشانی و خدمات ایمنی",
	AgencyEnv:      "اداره حفاظت محیط زیست",
	AgencyHeritage: "اداره میراث فرهنگی",
	AgencyRoads:    "اداره راه و شهرسازی",
	AgencyJustice:  "مراجع قضایی",
}

// deemedApprovalAllowed — کدام استعلام‌ها تأیید ضمنی می‌پذیرند.
// معیار: استعلامی که پاسخ منفی‌اش صرفاً اداری است، نه ایمنی یا حفاظتی.
var deemedApprovalAllowed = map[string]bool{
	AgencyRegistry: true,
	AgencyWater:    true,
	AgencyPower:    true,
	AgencyGas:      true,
	AgencyTelecom:  true,
	AgencyRoads:    true,
	AgencyFire:     false,
	AgencyEnv:      false,
	AgencyHeritage: false,
	AgencyJustice:  false,
}

const (
	InqPending  = "PENDING"
	InqApproved = "APPROVED"
	InqRejected = "REJECTED"
	InqDeemed   = "DEEMED_APPROVED" // تأیید ضمنی به دلیل انقضای مهلت
	InqOverdue  = "OVERDUE"         // مهلت گذشته ولی تأیید ضمنی مجاز نیست
)

// InquiryRecord — یک استعلام.
type InquiryRecord struct {
	DocType     string `json:"docType"`
	PermitID    string `json:"permitId"`
	ParcelID    string `json:"parcelId"`
	Agency      string `json:"agency"`
	AgencyTitle string `json:"agencyTitle"`
	Status      string `json:"status"`
	Question    string `json:"question"`
	Answer      string `json:"answer"`
	RefNo       string `json:"refNo"`
	SentAt      int64  `json:"sentAt"`
	DueAt       int64  `json:"dueAt"`
	AnsweredAt  int64  `json:"answeredAt"`
	AnsweredBy  string `json:"answeredBy"`
	// Blocking — پاسخ منفی این استعلام پرونده را متوقف می‌کند.
	Blocking bool `json:"blocking"`
}

func inqKey(permitID, agency string) string { return mkKey(KeyInquiry, permitID, agency) }

// Send ارسال استعلام با مهلت.
func (c *InquiryContract) Send(ctx contractapi.TransactionContextInterface,
	permitID, parcelID, agency, question string, slaDays int64) (*InquiryRecord, error) {

	if err := requireRole(ctx, RoleDistrict, RoleRegulator); err != nil {
		return nil, err
	}
	if _, ok := agencyTitle[agency]; !ok {
		return nil, fmt.Errorf("دستگاه استعلام‌شونده ناشناخته: «%s»", agency)
	}
	if slaDays <= 0 {
		slaDays = 10
	}
	now := txTime(ctx)
	r := InquiryRecord{
		DocType: "inquiry", PermitID: permitID, ParcelID: parcelID,
		Agency: agency, AgencyTitle: agencyTitle[agency], Status: InqPending,
		Question: question, SentAt: now, DueAt: now + slaDays*SecondsPerDay,
		Blocking: !deemedApprovalAllowed[agency],
	}
	if err := putJSON(ctx, inqKey(permitID, agency), &r); err != nil {
		return nil, err
	}
	if err := emit(ctx, "InquirySent", permitID,
		fmt.Sprintf("استعلام از %s برای پروانه %s ارسال شد؛ مهلت %d روز", agencyTitle[agency], permitID, slaDays),
		slaDays); err != nil {
		return nil, err
	}
	return &r, nil
}

// SendBatch ارسال گروهی استعلام‌ها. مجموعه دستگاه‌ها از شرایط پلاک استنتاج
// می‌شود، نه از انتخاب دستی کارشناس — تا هیچ استعلام لازمی جا نیفتد و هیچ
// استعلام غیرلازمی وقت نگیرد.
func (c *InquiryContract) SendBatch(ctx contractapi.TransactionContextInterface,
	permitID, parcelID string, heritageBuffer, isGarden bool, totalArea, slaDays int64) ([]InquiryRecord, error) {

	if err := requireRole(ctx, RoleDistrict, RoleRegulator); err != nil {
		return nil, err
	}
	agencies := []string{AgencyRegistry, AgencyWater, AgencyPower, AgencyGas}
	if heritageBuffer {
		agencies = append(agencies, AgencyHeritage)
	}
	if isGarden {
		agencies = append(agencies, AgencyEnv)
	}
	// طبق مقررات ملی ساختمان، ساختمان‌های بزرگ استعلام ایمنی جدا می‌خواهند.
	if totalArea >= 2000 {
		agencies = append(agencies, AgencyFire)
	}

	out := []InquiryRecord{}
	for _, a := range agencies {
		q := fmt.Sprintf("استعلام وضعیت پلاک %s جهت صدور پروانه ساختمانی", parcelID)
		r, err := c.Send(ctx, permitID, parcelID, a, q, slaDays)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	if err := emit(ctx, "InquiryBatchSent", permitID,
		fmt.Sprintf("%d استعلام برای پروانه %s ارسال شد", len(agencies), permitID), int64(len(agencies))); err != nil {
		return nil, err
	}
	return out, nil
}

// Answer پاسخ دستگاه. فقط نقش دستگاه استعلام‌شونده یا تنظیم‌گر.
func (c *InquiryContract) Answer(ctx contractapi.TransactionContextInterface,
	permitID, agency, verdict, answer, refNo string) (*InquiryRecord, error) {

	if err := requireRole(ctx, RoleUtility, RoleRegulator); err != nil {
		return nil, err
	}
	var r InquiryRecord
	if err := mustGetJSON(ctx, inqKey(permitID, agency), &r, "استعلام"); err != nil {
		return nil, err
	}
	if r.Status == InqApproved || r.Status == InqRejected {
		return nil, fmt.Errorf("این استعلام قبلاً پاسخ داده شده است")
	}
	switch verdict {
	case "APPROVE":
		r.Status = InqApproved
	case "REJECT":
		r.Status = InqRejected
	default:
		return nil, fmt.Errorf("نتیجه نامعتبر: «%s». مقادیر مجاز: APPROVE، REJECT", verdict)
	}
	now := txTime(ctx)
	r.Answer = answer
	r.RefNo = refNo
	r.AnsweredAt = now
	r.AnsweredBy = callerID(ctx)
	if err := putJSON(ctx, inqKey(permitID, agency), &r); err != nil {
		return nil, err
	}
	late := ""
	if now > r.DueAt {
		late = fmt.Sprintf(" (با %d روز تأخیر)", (now-r.DueAt)/SecondsPerDay)
	}
	if err := emit(ctx, "InquiryAnswered", permitID,
		fmt.Sprintf("پاسخ %s به استعلام پروانه %s: %s%s — %s",
			r.AgencyTitle, permitID, verdict, late, answer), 0); err != nil {
		return nil, err
	}
	return &r, nil
}

// SweepDeadlines اعمال تأیید ضمنی روی استعلام‌های منقضی.
//
// این تابع را زمان‌بند لایه API روزانه صدا می‌زند. زنجیره تایمر ندارد — و
// نباید داشته باشد؛ هر منطق زمان‌محورِ خودکار روی زنجیره یعنی هر peer باید
// در لحظه یکسانی همان تصمیم را بگیرد، که ممکن نیست.
func (c *InquiryContract) SweepDeadlines(ctx contractapi.TransactionContextInterface,
	permitID string) ([]InquiryRecord, error) {

	if err := requireRole(ctx, RoleRegulator, RoleDistrict, RoleAuditor); err != nil {
		return nil, err
	}
	raw, err := queryByPrefix(ctx, mkKey(KeyInquiry, permitID))
	if err != nil {
		return nil, err
	}
	now := txTime(ctx)
	out := []InquiryRecord{}
	for _, b := range raw {
		var r InquiryRecord
		if err := json.Unmarshal(b, &r); err != nil {
			continue
		}
		if r.Status != InqPending || now <= r.DueAt {
			continue
		}
		days := (now - r.DueAt) / SecondsPerDay
		if deemedApprovalAllowed[r.Agency] {
			r.Status = InqDeemed
			r.Answer = fmt.Sprintf("تأیید ضمنی به دلیل عدم پاسخ ظرف مهلت مقرر (%d روز تأخیر)", days)
			r.AnsweredAt = now
			if err := emit(ctx, "InquiryDeemedApproved", permitID,
				fmt.Sprintf("استعلام %s برای پروانه %s پس از %d روز تأخیر تأیید ضمنی شد",
					r.AgencyTitle, permitID, days), days); err != nil {
				return nil, err
			}
		} else {
			r.Status = InqOverdue
			if err := emit(ctx, "InquiryOverdue", permitID,
				fmt.Sprintf("⚠️ استعلام %s برای پروانه %s %d روز از مهلت گذشته است و تأیید ضمنی نمی‌پذیرد",
					r.AgencyTitle, permitID, days), days); err != nil {
				return nil, err
			}
		}
		if err := putJSON(ctx, inqKey(permitID, r.Agency), &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// AllCleared — شرط عبور پرونده از مرحله استعلام. از قرارداد Permit صدا زده
// می‌شود و رشته "true"/"false" برمی‌گرداند.
func (c *InquiryContract) AllCleared(ctx contractapi.TransactionContextInterface,
	permitID string) (string, error) {

	raw, err := queryByPrefix(ctx, mkKey(KeyInquiry, permitID))
	if err != nil {
		return "false", err
	}
	if len(raw) == 0 {
		// هیچ استعلامی ارسال نشده — یعنی هنوز به این مرحله نرسیده.
		return "false", nil
	}
	for _, b := range raw {
		var r InquiryRecord
		if err := json.Unmarshal(b, &r); err != nil {
			return "false", err
		}
		if r.Status != InqApproved && r.Status != InqDeemed {
			return "false", nil
		}
	}
	return "true", nil
}

func (c *InquiryContract) ListForPermit(ctx contractapi.TransactionContextInterface,
	permitID string) ([]InquiryRecord, error) {
	raw, err := queryByPrefix(ctx, mkKey(KeyInquiry, permitID))
	if err != nil {
		return nil, err
	}
	out := []InquiryRecord{}
	for _, b := range raw {
		var r InquiryRecord
		if err := json.Unmarshal(b, &r); err == nil {
			out = append(out, r)
		}
	}
	return out, nil
}

// AgencyPerformance کارنامه دستگاه‌های استعلام‌شونده.
//
// این گزارش سیاسی‌ترین خروجی شبکه است و به همین دلیل مهم‌ترین آن: تا وقتی
// «کدام دستگاه پرونده‌ها را معطل می‌کند» یک گمان باشد، هیچ اصلاحی ممکن
// نیست. با عدد، بحث از سرزنش به مدیریت منتقل می‌شود.
type AgencyStat struct {
	Agency       string `json:"agency"`
	Title        string `json:"title"`
	Total        int64  `json:"total"`
	Answered     int64  `json:"answered"`
	Pending      int64  `json:"pending"`
	Deemed       int64  `json:"deemed"`
	Overdue      int64  `json:"overdue"`
	Rejected     int64  `json:"rejected"`
	AvgDays      int64  `json:"avgDays"`
	OnTimePerMille int64 `json:"onTimePerMille"`
}

func (c *InquiryContract) AgencyPerformance(ctx contractapi.TransactionContextInterface) ([]AgencyStat, error) {
	raw, err := queryByPrefix(ctx, KeyInquiry)
	if err != nil {
		return nil, err
	}
	agg := map[string]*AgencyStat{}
	sumDays := map[string]int64{}
	onTime := map[string]int64{}

	for _, b := range raw {
		var r InquiryRecord
		if err := json.Unmarshal(b, &r); err != nil {
			continue
		}
		s, ok := agg[r.Agency]
		if !ok {
			s = &AgencyStat{Agency: r.Agency, Title: r.AgencyTitle}
			agg[r.Agency] = s
		}
		s.Total++
		switch r.Status {
		case InqPending:
			s.Pending++
		case InqDeemed:
			s.Deemed++
		case InqOverdue:
			s.Overdue++
		case InqRejected:
			s.Rejected++
			s.Answered++
		case InqApproved:
			s.Answered++
		}
		if r.AnsweredAt > 0 && r.Status != InqDeemed {
			sumDays[r.Agency] += (r.AnsweredAt - r.SentAt) / SecondsPerDay
			if r.AnsweredAt <= r.DueAt {
				onTime[r.Agency]++
			}
		}
	}

	// ترتیب ثابت: همان ترتیب اعلام ثابت‌ها.
	order := []string{AgencyRegistry, AgencyWater, AgencyPower, AgencyGas,
		AgencyTelecom, AgencyFire, AgencyEnv, AgencyHeritage, AgencyRoads, AgencyJustice}
	out := []AgencyStat{}
	for _, a := range order {
		s, ok := agg[a]
		if !ok {
			continue
		}
		if s.Answered > 0 {
			s.AvgDays = sumDays[a] / s.Answered
			s.OnTimePerMille = ratioPerMille(onTime[a], s.Answered)
		}
		out = append(out, *s)
	}
	return out, nil
}

func main() {
	cc, err := contractapi.NewChaincode(&InquiryContract{})
	if err != nil {
		log.Panicf("خطا در ساخت قرارداد Inquiry: %v", err)
	}
	if err := cc.Start(); err != nil {
		log.Panicf("خطا در اجرای قرارداد Inquiry: %v", err)
	}
}
