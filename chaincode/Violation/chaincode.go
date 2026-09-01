package main

// ---------------------------------------------------------------------------
// Violation — تخلفات ساختمانی و کمیسیون ماده ۱۰۰
//
// ── مرزی که این قرارداد عمداً از آن عبور نمی‌کند ──────────────────────────
//
// قانون به کمیسیون ماده ۱۰۰ اختیار می‌دهد بین تخریب و جریمه انتخاب کند، و
// اگر جریمه را انتخاب کرد، مبلغ را در بازه‌ای بین نصف تا سه برابر ارزش
// معاملاتی هر متر بنای اضافی تعیین کند. آن انتخاب یک قضاوت است و قرارداد
// هوشمند نباید جای آن بنشیند.
//
// پس این قرارداد رأی نمی‌دهد. کاری که می‌کند این است:
//   — پرونده را کامل و در لحظه تشکیل می‌دهد (نه ماه‌ها بعد)
//   — مبلغ پیشنهادی را در همان بازه قانونی و بر پایه شدت تخلف حساب می‌کند
//   — بازه قانونی را به‌عنوان قید سخت اعمال می‌کند: رأی خارج از بازه اصلاً
//     تراکنش معتبری نیست، هر که امضایش کند
//   — رأی، مبلغ، و نام رأی‌دهنده را تغییرناپذیر ثبت می‌کند
//
// یعنی اختیار کمیسیون دست‌نخورده می‌ماند، ولی حاشیه‌ای که در آن اختیار قابل
// سوءاستفاده است بسته می‌شود.
// ---------------------------------------------------------------------------

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/hyperledger/fabric-contract-api-go/contractapi"
)

type ViolationContract struct {
	NetworkBase
}

func call(ctx contractapi.TransactionContextInterface, cc, fn string, args ...string) ([]byte, error) {
	payload := [][]byte{[]byte(fn)}
	for _, a := range args {
		payload = append(payload, []byte(a))
	}
	resp := ctx.GetStub().InvokeChaincode(cc, payload, "")
	if resp.Status != 200 {
		return nil, fmt.Errorf("فراخوانی %s.%s ناموفق: %s", cc, fn, resp.Message)
	}
	return resp.Payload, nil
}

// انواع تخلف
const (
	VioExcessArea   = "EXCESS_AREA"   // بنای اضافی بر پروانه
	VioUseChange    = "USE_CHANGE"    // تغییر کاربری بدون مجوز
	VioNoPermit     = "NO_PERMIT"     // ساخت بدون پروانه
	VioSetback      = "SETBACK"       // عدم رعایت عقب‌نشینی
	VioParking      = "PARKING"       // حذف پارکینگ
	VioExtraFloor   = "EXTRA_FLOOR"   // طبقه مازاد
	VioGardenDamage = "GARDEN_DAMAGE" // تخریب باغ
)

var vioTitle = map[string]string{
	VioExcessArea: "بنای اضافی بر مفاد پروانه",
	VioUseChange:  "تغییر کاربری بدون مجوز",
	VioNoPermit:   "احداث بنا بدون پروانه",
	VioSetback:    "عدم رعایت عقب‌نشینی",
	VioParking:    "حذف یا کسری پارکینگ",
	VioExtraFloor: "احداث طبقه مازاد",
	VioGardenDamage: "تخریب یا تصرف باغ",
}

const (
	VioOpen     = "OPEN"     // تشکیل‌شده، در انتظار کمیسیون
	VioRuled    = "RULED"    // رأی صادر شده
	VioSettled  = "SETTLED"  // جریمه وصول شد
	VioDemolish = "DEMOLISH" // رأی به تخریب
	VioDismissed = "DISMISSED" // مختومه بدون تخلف
)

// ViolationCase — پرونده تخلف.
type ViolationCase struct {
	DocType   string `json:"docType"`
	ID        string `json:"id"`
	PermitID  string `json:"permitId"`
	ParcelID  string `json:"parcelId"`
	Region    string `json:"region"`
	Kind      string `json:"kind"`
	KindTitle string `json:"kindTitle"`
	Status    string `json:"status"`
	// ExcessArea — متراژ بنای اضافی، مبنای محاسبه جریمه.
	ExcessArea int64 `json:"excessArea"`
	AllowedArea int64 `json:"allowedArea"`
	PriceZonal  int64 `json:"priceZonal"`
	// SuggestedFine و بازه قانونی — پیشنهاد قرارداد، نه رأی.
	SuggestedFine     int64 `json:"suggestedFine"`
	SuggestedPerMille int64 `json:"suggestedPerMille"`
	MinFine           int64 `json:"minFine"`
	MaxFine           int64 `json:"maxFine"`
	// RuledFine — رأی واقعی کمیسیون.
	RuledFine    int64  `json:"ruledFine"`
	RuledBy      string `json:"ruledBy"`
	RuledAt      int64  `json:"ruledAt"`
	RulingNote   string `json:"rulingNote"`
	Evidence     string `json:"evidence"`
	OpenedBy     string `json:"openedBy"`
	OpenedAt     int64  `json:"openedAt"`
	UpdatedAt    int64  `json:"updatedAt"`
}

// OpenViolation تشکیل پرونده تخلف. معمولاً به‌صورت خودکار از قرارداد Permit
// هنگام ثبت گزارش بازدید فراخوانی می‌شود.
func (c *ViolationContract) OpenViolation(ctx contractapi.TransactionContextInterface,
	vioID, permitID, kind string, excessArea int64, evidence string) (*ViolationCase, error) {

	if _, ok := vioTitle[kind]; !ok {
		return nil, fmt.Errorf("نوع تخلف ناشناخته: «%s»", kind)
	}
	// اگر پرونده باز است، همان را با شواهد جدید به‌روز می‌کنیم؛ تشکیل پرونده
	// تکراری برای یک ساختمان فقط آمار را متورم می‌کند.
	var v ViolationCase
	exists, err := getJSON(ctx, mkKey(KeyViolation, vioID), &v)
	if err != nil {
		return nil, err
	}
	if exists && v.Status != VioOpen {
		return nil, fmt.Errorf("پرونده تخلف «%s» در وضعیت «%s» است و قابل بازگشایی نیست", vioID, v.Status)
	}

	pb, err := call(ctx, "Permit", "GetPermit", permitID)
	if err != nil {
		return nil, err
	}
	var p Permit
	if err := json.Unmarshal(pb, &p); err != nil {
		return nil, err
	}
	rb, err := call(ctx, "Regulation", "GetRegion", p.Region)
	if err != nil {
		return nil, err
	}
	var reg Region
	if err := json.Unmarshal(rb, &reg); err != nil {
		return nil, err
	}

	// قیمت منطقه‌ای کاربری غالب ساختمان مبنا قرار می‌گیرد.
	dominant := UseResidential
	best := int64(0)
	for _, u := range AllUses {
		area := int64(0)
		for _, f := range p.Floors {
			if f.Use == u {
				area += f.Area
			}
		}
		if area > best {
			best = area
			dominant = u
		}
	}
	price := reg.PriceZonal[dominant]
	if price == 0 {
		price = reg.PriceZonal[UseResidential]
	}

	// بازه قانونی از دفترچه تعرفه سال جاری. اگر در دسترس نبود، کف و سقف
	// قانونی (نصف تا سه برابر) اعمال می‌شود.
	minPM, maxPM := int64(500), int64(3000)
	year := 1970 + txTime(ctx)/SecondsPerYear
	if tb, e := call(ctx, "Regulation", "GetTariff", fmt.Sprintf("%d", year)); e == nil {
		var t Tariff
		if json.Unmarshal(tb, &t) == nil && t.Article100MinPerMille > 0 {
			minPM, maxPM = t.Article100MinPerMille, t.Article100MaxPerMille
		}
	}
	fine, pm := article100Penalty(excessArea, price, minPM, maxPM, p.AllowedArea)

	now := txTime(ctx)
	v = ViolationCase{
		DocType: "violation", ID: vioID, PermitID: permitID, ParcelID: p.ParcelID,
		Region: p.Region, Kind: kind, KindTitle: vioTitle[kind], Status: VioOpen,
		ExcessArea: excessArea, AllowedArea: p.AllowedArea, PriceZonal: price,
		SuggestedFine: fine, SuggestedPerMille: pm,
		MinFine: excessArea * price * minPM / 1000,
		MaxFine: excessArea * price * maxPM / 1000,
		Evidence: evidence, OpenedBy: callerID(ctx), OpenedAt: now, UpdatedAt: now,
	}
	if err := putJSON(ctx, mkKey(KeyViolation, vioID), &v); err != nil {
		return nil, err
	}
	if err := emit(ctx, "ViolationOpened", vioID,
		fmt.Sprintf("پرونده تخلف %s — %s، %d م² اضافی روی پروانه %s. پیشنهاد جریمه %d ریال (بازه قانونی %d تا %d)",
			vioID, vioTitle[kind], excessArea, permitID, fine, v.MinFine, v.MaxFine), fine); err != nil {
		return nil, err
	}
	return &v, nil
}

// Rule صدور رأی کمیسیون ماده ۱۰۰.
//
// دو قید سخت: فقط نقش کمیسیون، و مبلغ حتماً داخل بازه قانونی. رأی خارج از
// بازه تراکنش نامعتبر است — نه هشدار، نه لاگ، بلکه رد.
func (c *ViolationContract) Rule(ctx contractapi.TransactionContextInterface,
	vioID, decision string, fine int64, note string) (*ViolationCase, error) {

	if err := requireRole(ctx, RoleCommission); err != nil {
		return nil, err
	}
	var v ViolationCase
	if err := mustGetJSON(ctx, mkKey(KeyViolation, vioID), &v, "پرونده تخلف"); err != nil {
		return nil, err
	}
	if v.Status != VioOpen {
		return nil, fmt.Errorf("پرونده «%s» در وضعیت «%s» است و رأی جدید نمی‌پذیرد", vioID, v.Status)
	}
	now := txTime(ctx)

	switch decision {
	case "FINE":
		if fine < v.MinFine || fine > v.MaxFine {
			return nil, fmt.Errorf(
				"مبلغ رأی (%d ریال) خارج از بازه قانونی است. بازه مجاز برای %d م² بنای اضافی: %d تا %d ریال",
				fine, v.ExcessArea, v.MinFine, v.MaxFine)
		}
		v.Status = VioRuled
		v.RuledFine = fine
		// جریمه به صورت‌حساب همان پروانه اضافه می‌شود تا وصولش به همان
		// سازوکار پرداخت و اقساط وصل باشد، نه یک مسیر جدا.
		if _, err := call(ctx, "Fee", "AddLine", v.PermitID, "ART100",
			"جریمه کمیسیون ماده ۱۰۰",
			fmt.Sprintf("%d م² بنای اضافی × %d ریال — رأی کمیسیون", v.ExcessArea, v.PriceZonal),
			fmt.Sprintf("%d", fine)); err != nil {
			return nil, err
		}
	case "DEMOLISH":
		v.Status = VioDemolish
		v.RuledFine = 0
	case "DISMISS":
		v.Status = VioDismissed
		v.RuledFine = 0
	default:
		return nil, fmt.Errorf("رأی نامعتبر: «%s». مقادیر مجاز: FINE، DEMOLISH، DISMISS", decision)
	}

	v.RuledBy = callerID(ctx)
	v.RuledAt = now
	v.RulingNote = note
	v.UpdatedAt = now
	if err := putJSON(ctx, mkKey(KeyViolation, vioID), &v); err != nil {
		return nil, err
	}
	if err := emit(ctx, "ViolationRuled", vioID,
		fmt.Sprintf("رأی کمیسیون بر پرونده %s: %s، مبلغ %d ریال (پیشنهاد سامانه %d) — %s",
			vioID, decision, v.RuledFine, v.SuggestedFine, note), v.RuledFine); err != nil {
		return nil, err
	}
	return &v, nil
}

// Settle مختومه کردن پرونده پس از وصول جریمه.
func (c *ViolationContract) Settle(ctx contractapi.TransactionContextInterface,
	vioID string) (*ViolationCase, error) {

	if err := requireRole(ctx, RoleFinance, RoleRegulator); err != nil {
		return nil, err
	}
	var v ViolationCase
	if err := mustGetJSON(ctx, mkKey(KeyViolation, vioID), &v, "پرونده تخلف"); err != nil {
		return nil, err
	}
	if v.Status != VioRuled {
		return nil, fmt.Errorf("فقط پرونده دارای رأی جریمه قابل تسویه است (وضعیت فعلی: %s)", v.Status)
	}
	sb, err := call(ctx, "Treasury", "IsSettled", v.PermitID)
	if err != nil {
		return nil, err
	}
	if string(sb) != "true" {
		return nil, fmt.Errorf("جریمه هنوز وصول نشده است")
	}
	v.Status = VioSettled
	v.UpdatedAt = txTime(ctx)
	if err := putJSON(ctx, mkKey(KeyViolation, vioID), &v); err != nil {
		return nil, err
	}
	if err := emit(ctx, "ViolationSettled", vioID,
		fmt.Sprintf("پرونده تخلف %s با وصول %d ریال مختومه شد", vioID, v.RuledFine), v.RuledFine); err != nil {
		return nil, err
	}
	return &v, nil
}

// IsResolved شرط صدور پایان‌کار — از قرارداد Permit صدا زده می‌شود.
func (c *ViolationContract) IsResolved(ctx contractapi.TransactionContextInterface,
	vioID string) (string, error) {
	var v ViolationCase
	ok, err := getJSON(ctx, mkKey(KeyViolation, vioID), &v)
	if err != nil {
		return "false", err
	}
	if !ok {
		// پرونده‌ای وجود ندارد یعنی تخلفی ثبت نشده.
		return "true", nil
	}
	if v.Status == VioSettled || v.Status == VioDismissed {
		return "true", nil
	}
	return "false", nil
}

func (c *ViolationContract) GetViolation(ctx contractapi.TransactionContextInterface,
	vioID string) (*ViolationCase, error) {
	var v ViolationCase
	if err := mustGetJSON(ctx, mkKey(KeyViolation, vioID), &v, "پرونده تخلف"); err != nil {
		return nil, err
	}
	return &v, nil
}

func (c *ViolationContract) ListViolations(ctx contractapi.TransactionContextInterface,
	region, status string) ([]ViolationCase, error) {
	raw, err := queryByPrefix(ctx, KeyViolation)
	if err != nil {
		return nil, err
	}
	out := []ViolationCase{}
	for _, b := range raw {
		var v ViolationCase
		if err := json.Unmarshal(b, &v); err != nil {
			continue
		}
		if region != "" && v.Region != region {
			continue
		}
		if status != "" && v.Status != status {
			continue
		}
		out = append(out, v)
	}
	return out, nil
}

// RulingConsistency گزارش انحراف آرای کمیسیون از پیشنهاد سامانه.
//
// این گزارش ابزار حسابرسی است، نه ابزار قضاوت. انحراف به‌خودی‌خود ایراد
// نیست — کمیسیون حق دارد و باید از پیشنهاد یک فرمول فاصله بگیرد وقتی پرونده
// شرایط خاصی دارد. چیزی که این گزارش نشان می‌دهد الگو است: اگر در یک منطقه
// همه آرا نزدیک کف بازه باشند و در منطقه دیگر نزدیک سقف، آن یک پرسش است که
// باید پرسیده شود.
type ConsistencyRow struct {
	Region          string `json:"region"`
	Cases           int64  `json:"cases"`
	TotalSuggested  int64  `json:"totalSuggested"`
	TotalRuled      int64  `json:"totalRuled"`
	DeviationPerMille int64 `json:"deviationPerMille"`
	DemolishCount   int64  `json:"demolishCount"`
	DismissCount    int64  `json:"dismissCount"`
}

func (c *ViolationContract) RulingConsistency(ctx contractapi.TransactionContextInterface) ([]ConsistencyRow, error) {
	if err := requireRole(ctx, RoleAuditor, RoleRegulator, RoleCommission); err != nil {
		return nil, err
	}
	raw, err := queryByPrefix(ctx, KeyViolation)
	if err != nil {
		return nil, err
	}
	agg := map[string]*ConsistencyRow{}
	for _, b := range raw {
		var v ViolationCase
		if err := json.Unmarshal(b, &v); err != nil {
			continue
		}
		if v.Status == VioOpen {
			continue
		}
		r, ok := agg[v.Region]
		if !ok {
			r = &ConsistencyRow{Region: v.Region}
			agg[v.Region] = r
		}
		r.Cases++
		r.TotalSuggested += v.SuggestedFine
		r.TotalRuled += v.RuledFine
		if v.Status == VioDemolish {
			r.DemolishCount++
		}
		if v.Status == VioDismissed {
			r.DismissCount++
		}
	}
	// خروجی به ترتیب ثابت مناطق تا نتیجه قطعی باشد.
	out := []ConsistencyRow{}
	for i := 1; i <= 11; i++ {
		key := fmt.Sprintf("%d", i)
		if r, ok := agg[key]; ok {
			r.DeviationPerMille = ratioPerMille(r.TotalRuled, r.TotalSuggested)
			out = append(out, *r)
		}
	}
	return out, nil
}

func main() {
	cc, err := contractapi.NewChaincode(&ViolationContract{})
	if err != nil {
		log.Panicf("خطا در ساخت قرارداد Violation: %v", err)
	}
	if err := cc.Start(); err != nil {
		log.Panicf("خطا در اجرای قرارداد Violation: %v", err)
	}
}
