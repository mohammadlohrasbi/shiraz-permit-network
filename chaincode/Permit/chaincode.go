package main

// ---------------------------------------------------------------------------
// Permit — قرارداد چرخه عمر پروانه ساختمانی
//
// این قرارداد ارکستر شبکه است: با Regulation، Parcel، Fee، SqmToken و
// Treasury روی همان کانال گفت‌وگو می‌کند تا صدور پروانه یک تراکنش اتمی باشد،
// نه زنجیره‌ای از گام‌های نیمه‌کاره.
//
// ── چرا این اتمی بودن مهم است ─────────────────────────────────────────────
// در فرآیند فعلی، «پروانه صادر شد» و «عوارض وصول شد» و «تراکم مصرف شد» سه
// رویداد در سه سامانه‌اند. هر ناهماهنگی بینشان یا به ضرر شهروند تمام می‌شود
// یا به ضرر شهرداری، و کشف اینکه کدام‌یک اول اشتباه شد ماه‌ها طول می‌کشد.
// وقتی هر سه در یک تراکنش‌اند، حالت ناسازگار اصلاً وجود ندارد: یا همه اتفاق
// می‌افتند یا هیچ‌کدام.
//
// ⚠️ محدودیتی که این طراحی را تحمیل کرد: InvokeChaincode روی کانالِ دیگر
// فقط خواندنی است و read-set آن اعتبارسنجی نمی‌شود. پس همه قراردادهای
// هسته باید روی یک کانال باشند. کانال دوم برای این کار جواب نمی‌دهد.
// ---------------------------------------------------------------------------

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/hyperledger/fabric-contract-api-go/contractapi"
)

type PermitContract struct {
	NetworkBase
}

// call یک قرارداد دیگر روی همان کانال را صدا می‌زند و خطای گویا برمی‌گرداند.
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

// ---------------------------- ایجاد پرونده ----------------------------

// CreatePermit تشکیل پرونده پروانه.
//
// همین‌جا و نه بعداً، درخواست در برابر ضوابط پهنه سنجیده می‌شود. علت: پرونده‌ای
// که از روز اول می‌دانیم رد می‌شود، نباید ماه‌ها در صف بماند تا در مرحله
// کارشناسی کشف شود.
func (c *PermitContract) CreatePermit(ctx contractapi.TransactionContextInterface,
	permitID, parcelID, floorsJSON string,
	parkingProvided int64, engineerID, designerID string, greenCertified bool) (*Permit, error) {

	if err := requireRole(ctx, RoleCitizen, RoleDistrict, RoleRegulator); err != nil {
		return nil, err
	}
	exists, err := ctx.GetStub().GetState(mkKey(KeyPermit, permitID))
	if err != nil {
		return nil, err
	}
	if exists != nil {
		return nil, fmt.Errorf("پروانه با شناسه «%s» قبلاً ثبت شده است", permitID)
	}

	pb, err := call(ctx, "Parcel", "GetParcel", parcelID)
	if err != nil {
		return nil, err
	}
	var parcel Parcel
	if err := json.Unmarshal(pb, &parcel); err != nil {
		return nil, fmt.Errorf("خطا در خواندن پلاک: %v", err)
	}
	if parcel.Blocked {
		return nil, fmt.Errorf("پلاک «%s» در بازداشت است: %s", parcelID, parcel.BlockNote)
	}

	var floors []FloorSpec
	if err := json.Unmarshal([]byte(floorsJSON), &floors); err != nil {
		return nil, fmt.Errorf("مشخصات طبقات نامعتبر است: %v", err)
	}
	if len(floors) == 0 {
		return nil, fmt.Errorf("حداقل یک طبقه باید تعریف شود")
	}

	// مساحت مؤثر = عرصه منهای عقب‌نشینی. عقب‌نشینی متری است که مالک به معبر
	// می‌دهد و نباید مبنای تراکم قرار بگیرد.
	effectiveLand := parcel.LandArea - parcel.SetbackArea
	if effectiveLand <= 0 {
		return nil, fmt.Errorf("پس از کسر عقب‌نشینی، مساحت قابل ساختی باقی نمی‌ماند")
	}

	vb, err := call(ctx, "Regulation", "EvaluateZoning",
		parcel.Zone, fmt.Sprintf("%d", effectiveLand), floorsJSON)
	if err != nil {
		return nil, err
	}
	var verdict ZoningVerdict
	if err := json.Unmarshal(vb, &verdict); err != nil {
		return nil, fmt.Errorf("خطا در ارزیابی ضوابط: %v", err)
	}
	if len(verdict.Violations) > 0 {
		return nil, fmt.Errorf("درخواست با ضوابط پهنه منطبق نیست:\n— %s",
			joinLines(verdict.Violations))
	}

	total, units := int64(0), int64(0)
	for _, f := range floors {
		if f.Area < 0 || f.Units < 0 {
			return nil, fmt.Errorf("مساحت و تعداد واحد نمی‌توانند منفی باشند")
		}
		total += f.Area
		units += f.Units
	}
	// یک واحد یک پارکینگ؛ واحد تجاری بالای ۵۰ متر دو پارکینگ.
	required := int64(0)
	for _, f := range floors {
		if f.Use == UseCommercial && f.Area >= 50 {
			required += f.Units * 2
		} else {
			required += f.Units
		}
	}

	now := txTime(ctx)
	p := Permit{
		DocType: "permit", ID: permitID, ParcelID: parcelID,
		Region: parcel.Region, Zone: parcel.Zone, OwnerID: parcel.OwnerID,
		Status: StDraft, Floors: floors, TotalArea: total,
		AllowedArea: verdict.AllowedArea, ExcessArea: verdict.ExcessArea,
		ParkingRequired: required, ParkingProvided: parkingProvided,
		GreenCertified: greenCertified, EngineerID: engineerID, DesignerID: designerID,
		TokenLocked: map[string]int64{},
		History: []Transition{{
			From: "", To: StDraft, By: callerID(ctx), MSP: callerMSP(ctx),
			Note: "تشکیل پرونده", At: now, TxID: ctx.GetStub().GetTxID(),
		}},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := putJSON(ctx, mkKey(KeyPermit, permitID), &p); err != nil {
		return nil, err
	}
	if err := emit(ctx, "PermitCreated", permitID,
		fmt.Sprintf("پرونده پروانه %s روی پلاک %s — زیربنای درخواستی %d م² (مجاز %d، مازاد %d)",
			permitID, parcelID, total, verdict.AllowedArea, verdict.ExcessArea), total); err != nil {
		return nil, err
	}
	return &p, nil
}

func (c *PermitContract) GetPermit(ctx contractapi.TransactionContextInterface,
	id string) (*Permit, error) {
	var p Permit
	if err := mustGetJSON(ctx, mkKey(KeyPermit, id), &p, "پروانه"); err != nil {
		return nil, err
	}
	return &p, nil
}

// ---------------------------- ماشین حالت ----------------------------

// Advance گذار وضعیت. هر گذار در برابر جدول allowedTransitions سنجیده
// می‌شود و برخی گذارها شرط اضافه هم دارند.
func (c *PermitContract) Advance(ctx contractapi.TransactionContextInterface,
	permitID, to, note string) (*Permit, error) {

	var p Permit
	if err := mustGetJSON(ctx, mkKey(KeyPermit, permitID), &p, "پروانه"); err != nil {
		return nil, err
	}
	if err := canTransition(ctx, p.Status, to); err != nil {
		return nil, err
	}

	switch to {
	case StDesign:
		// از استعلام به بررسی نقشه فقط وقتی همه استعلام‌ها تعیین تکلیف شده‌اند.
		ok, err := inquiriesCleared(ctx, permitID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("هنوز استعلام بی‌پاسخ یا منفی وجود دارد؛ پرونده به مرحله بررسی نقشه نمی‌رود")
		}
	case StAppraisal:
		if p.ExcessArea > p.TdrCoveredArea && p.Status != StCommission5 {
			return nil, fmt.Errorf("مازاد تراکم %d م² هنوز پوشش داده نشده است؛ یا توکن حق توسعه تأمین کنید یا پرونده به کمیسیون ماده ۵ برود",
				p.ExcessArea-p.TdrCoveredArea)
		}
	case StIssued:
		return nil, fmt.Errorf("صدور پروانه با تابع Issue انجام می‌شود، نه با Advance")
	case StCompletion:
		return nil, fmt.Errorf("صدور پایان‌کار با تابع IssueCompletion انجام می‌شود")
	}

	from := p.Status
	p.Status = to
	p.UpdatedAt = txTime(ctx)
	p.History = append(p.History, Transition{
		From: from, To: to, By: callerID(ctx), MSP: callerMSP(ctx),
		Note: note, At: p.UpdatedAt, TxID: ctx.GetStub().GetTxID(),
	})
	if err := putJSON(ctx, mkKey(KeyPermit, permitID), &p); err != nil {
		return nil, err
	}
	if err := emit(ctx, "PermitAdvanced", permitID,
		fmt.Sprintf("پروانه %s: %s ← %s — %s", permitID, from, to, note), 0); err != nil {
		return nil, err
	}
	return &p, nil
}

// inquiriesCleared بررسی می‌کند همه استعلام‌های ثبت‌شده مثبت یا منقضی‌شده
// باشند. تأیید ضمنی پس از مهلت، در قرارداد Inquiry اعمال می‌شود.
func inquiriesCleared(ctx contractapi.TransactionContextInterface, permitID string) (bool, error) {
	b, err := call(ctx, "Inquiry", "AllCleared", permitID)
	if err != nil {
		// اگر قرارداد استعلام روی این کانال نصب نباشد، شرط را رد نمی‌کنیم و
		// اجازه ادامه می‌دهیم — ولی این را در رویداد ثبت می‌کنیم تا در
		// حسابرسی دیده شود.
		_ = emit(ctx, "InquiryCheckSkipped", permitID,
			"قرارداد استعلام در دسترس نبود؛ کنترل استعلام انجام نشد", 0)
		return true, nil
	}
	return string(b) == "true", nil
}

// ---------------------------- تراکم انتقالی ----------------------------

// ApplyTdr پوشش مازاد تراکم با توکن حق توسعه خریداری‌شده.
//
// اینجاست که توکن‌سازی از یک ایده به یک ابزار اداری تبدیل می‌شود: متقاضی
// به‌جای رفتن به کمیسیون و ماه‌ها انتظار، توکن معادل مازادش را از بازار
// می‌خرد و پرونده بی‌درنگ جلو می‌رود. شهرداری کارمزد معامله را می‌گیرد و
// تراکم از پهنه‌ای آمده که عمداً محدود شده است.
func (c *PermitContract) ApplyTdr(ctx contractapi.TransactionContextInterface,
	permitID, use string, amount int64) (*Permit, error) {

	if err := requireRole(ctx, RoleCitizen, RoleDistrict, RoleRegulator); err != nil {
		return nil, err
	}
	var p Permit
	if err := mustGetJSON(ctx, mkKey(KeyPermit, permitID), &p, "پروانه"); err != nil {
		return nil, err
	}
	if p.Status != StDraft && p.Status != StDesign && p.Status != StInquiry {
		return nil, fmt.Errorf("تأمین تراکم انتقالی فقط پیش از مرحله محاسبه عوارض ممکن است (وضعیت فعلی: %s)", p.Status)
	}
	need := p.ExcessArea - p.TdrCoveredArea
	if need <= 0 {
		return nil, fmt.Errorf("این پروانه مازاد تراکم پوشش‌نداده ندارد")
	}
	if amount > need {
		amount = need
	}
	// توکن از موجودی مالک قفل می‌شود. اگر کاربری توکن با کاربری مورد نیاز
	// فرق دارد، تبدیل ارزشی انجام می‌شود.
	if _, err := call(ctx, "SqmToken", "LockForPermit",
		permitID, use, p.OwnerID, fmt.Sprintf("%d", amount)); err != nil {
		return nil, err
	}
	p.TdrCoveredArea += amount
	if p.TokenLocked == nil {
		p.TokenLocked = map[string]int64{}
	}
	p.TokenLocked[use] += amount
	p.UpdatedAt = txTime(ctx)
	if err := putJSON(ctx, mkKey(KeyPermit, permitID), &p); err != nil {
		return nil, err
	}
	if err := emit(ctx, "TdrApplied", permitID,
		fmt.Sprintf("پوشش %d م² از مازاد تراکم پروانه %s با توکن %s (باقی‌مانده %d م²)",
			amount, permitID, UseTitleFa[use], p.ExcessArea-p.TdrCoveredArea), amount); err != nil {
		return nil, err
	}
	return &p, nil
}

// ---------------------------- محاسبه و صدور ----------------------------

// Appraise محاسبه عوارض و ساخت صورت‌حساب. پرونده به مرحله پرداخت می‌رود.
func (c *PermitContract) Appraise(ctx contractapi.TransactionContextInterface,
	permitID string, tariffYear int64, cashPay bool) (*Invoice, error) {

	if err := requireRole(ctx, RoleFinance, RoleRegulator, RoleDistrict); err != nil {
		return nil, err
	}
	var p Permit
	if err := mustGetJSON(ctx, mkKey(KeyPermit, permitID), &p, "پروانه"); err != nil {
		return nil, err
	}
	if p.Status != StAppraisal && p.Status != StDesign {
		return nil, fmt.Errorf("محاسبه عوارض در وضعیت «%s» مجاز نیست", p.Status)
	}

	cash := "false"
	if cashPay {
		cash = "true"
	}
	ib, err := call(ctx, "Fee", "BuildInvoice", permitID, fmt.Sprintf("%d", tariffYear), cash)
	if err != nil {
		return nil, err
	}
	var inv Invoice
	if err := json.Unmarshal(ib, &inv); err != nil {
		return nil, fmt.Errorf("خطا در خواندن صورت‌حساب: %v", err)
	}

	from := p.Status
	p.Status = StPayment
	p.InvoiceTotal = inv.Total
	p.UpdatedAt = txTime(ctx)
	p.History = append(p.History, Transition{
		From: from, To: StPayment, By: callerID(ctx), MSP: callerMSP(ctx),
		Note: fmt.Sprintf("عوارض محاسبه شد: %d ریال", inv.Total),
		At:   p.UpdatedAt, TxID: ctx.GetStub().GetTxID(),
	})
	if err := putJSON(ctx, mkKey(KeyPermit, permitID), &p); err != nil {
		return nil, err
	}
	if err := emit(ctx, "PermitAppraised", permitID,
		fmt.Sprintf("عوارض پروانه %s: %d ریال (%d ردیف، تخفیف %d)",
			permitID, inv.Total, len(inv.Lines), inv.Discount), inv.Total); err != nil {
		return nil, err
	}
	return &inv, nil
}

// Issue صدور پروانه. سه شرط هم‌زمان بررسی می‌شود و هر سه در یک تراکنش
// اعمال می‌شوند: تسویه عوارض، تأمین تراکم، و قفل توکن معادل کل زیربنا.
func (c *PermitContract) Issue(ctx contractapi.TransactionContextInterface,
	permitID string, validityMonths int64) (*Permit, error) {

	if err := requireRole(ctx, RoleRegulator, RoleFinance); err != nil {
		return nil, err
	}
	var p Permit
	if err := mustGetJSON(ctx, mkKey(KeyPermit, permitID), &p, "پروانه"); err != nil {
		return nil, err
	}
	if p.Status != StPayment {
		return nil, fmt.Errorf("صدور پروانه فقط از وضعیت «در انتظار پرداخت» ممکن است (وضعیت فعلی: %s)", p.Status)
	}

	// شرط ۱ — تسویه مالی
	sb, err := call(ctx, "Treasury", "IsSettled", permitID)
	if err != nil {
		return nil, err
	}
	if string(sb) != "true" {
		return nil, fmt.Errorf("عوارض پروانه %s تسویه نشده است؛ صدور ممکن نیست", permitID)
	}

	// شرط ۲ — پوشش کامل مازاد تراکم
	if p.ExcessArea > p.TdrCoveredArea {
		return nil, fmt.Errorf("مازاد تراکم %d م² پوشش داده نشده است", p.ExcessArea-p.TdrCoveredArea)
	}

	// شرط ۳ — قفل توکن معادل زیربنای مجازِ مصرف‌شده
	if err := lockPermitTokens(ctx, &p); err != nil {
		return nil, err
	}

	now := txTime(ctx)
	if validityMonths <= 0 {
		validityMonths = 24
	}
	from := p.Status
	p.Status = StIssued
	p.IssuedAt = now
	p.ExpiresAt = now + validityMonths*SecondsPerMonth
	p.UpdatedAt = now
	p.History = append(p.History, Transition{
		From: from, To: StIssued, By: callerID(ctx), MSP: callerMSP(ctx),
		Note: fmt.Sprintf("صدور پروانه با اعتبار %d ماه", validityMonths),
		At:   now, TxID: ctx.GetStub().GetTxID(),
	})
	if err := putJSON(ctx, mkKey(KeyPermit, permitID), &p); err != nil {
		return nil, err
	}
	if err := emit(ctx, "PermitIssued", permitID,
		fmt.Sprintf("پروانه %s صادر شد — پلاک %s، زیربنا %d م²، اعتبار تا %d",
			permitID, p.ParcelID, p.TotalArea, p.ExpiresAt), p.TotalArea); err != nil {
		return nil, err
	}
	return &p, nil
}

// lockPermitTokens توکن متراژ هر کاربری را به‌اندازه سهم آن از زیربنای مجاز
// قفل می‌کند. مازادی که با TDR پوشش داده شده از قبل قفل است.
func lockPermitTokens(ctx contractapi.TransactionContextInterface, p *Permit) error {
	if p.TokenLocked == nil {
		p.TokenLocked = map[string]int64{}
	}
	// سهم هر کاربری از زیربنای مجاز، به ترتیب ثابت AllUses تا نتیجه قطعی باشد.
	byUse := map[string]int64{}
	for _, f := range p.Floors {
		byUse[f.Use] += f.Area
	}
	remaining := p.AllowedArea
	for _, use := range AllUses {
		area := byUse[use]
		if area <= 0 || remaining <= 0 {
			continue
		}
		take := area
		if take > remaining {
			take = remaining
		}
		if !MintableUse[use] {
			return fmt.Errorf("کاربری «%s» توکن‌پذیر نیست و نمی‌تواند در پروانه بیاید", UseTitleFa[use])
		}
		if _, err := call(ctx, "SqmToken", "LockForPermit",
			p.ID, use, p.OwnerID, fmt.Sprintf("%d", take)); err != nil {
			return err
		}
		p.TokenLocked[use] += take
		remaining -= take
	}
	return nil
}

// ---------------------------- صدور خودکار ----------------------------

// FastTrackIssue صدور خودکار پروانه بدون دخالت کارشناس.
//
// هدف این تابع همان چیزی است که پروژه دنبالش است: یک ساختمان مسکونی کوچک،
// در پهنه‌ای که کاربری‌اش منطبق است، بدون مازاد تراکم، با پارکینگ کامل و
// عوارض پرداخت‌شده — هیچ قضاوت انسانی لازم ندارد. امروز همین پرونده هفته‌ها
// در صف کارشناسی می‌ماند، نه به‌خاطر پیچیدگی، بلکه به‌خاطر صف.
//
// شرایط عمداً سخت‌گیرانه‌اند. مسیر خودکار باید آن‌قدر امن باشد که کسی برای
// دور زدنش وسوسه نشود؛ هر پرونده‌ای که یکی از این شرط‌ها را نداشته باشد به
// مسیر عادی می‌رود، نه اینکه رد شود.
func (c *PermitContract) FastTrackIssue(ctx contractapi.TransactionContextInterface,
	permitID string, tariffYear int64) (*Permit, error) {

	if err := requireRole(ctx, RoleCitizen, RoleDistrict, RoleRegulator); err != nil {
		return nil, err
	}
	var p Permit
	if err := mustGetJSON(ctx, mkKey(KeyPermit, permitID), &p, "پروانه"); err != nil {
		return nil, err
	}
	tb, err := call(ctx, "Regulation", "GetTariff", fmt.Sprintf("%d", tariffYear))
	if err != nil {
		return nil, err
	}
	var tar Tariff
	if err := json.Unmarshal(tb, &tar); err != nil {
		return nil, err
	}

	reasons := []string{}
	if p.Status != StDraft && p.Status != StPayment {
		reasons = append(reasons, fmt.Sprintf("وضعیت پرونده «%s» است", p.Status))
	}
	if tar.FastTrackMaxArea > 0 && p.TotalArea > tar.FastTrackMaxArea {
		reasons = append(reasons, fmt.Sprintf("زیربنا %d م² از سقف مسیر سریع (%d م²) بیشتر است",
			p.TotalArea, tar.FastTrackMaxArea))
	}
	if p.ExcessArea > p.TdrCoveredArea {
		reasons = append(reasons, "مازاد تراکم پوشش‌نداده دارد")
	}
	if p.ParkingProvided < p.ParkingRequired {
		reasons = append(reasons, "کسری پارکینگ دارد")
	}
	for _, f := range p.Floors {
		if f.Use != UseResidential {
			reasons = append(reasons, "مسیر سریع فقط برای کاربری تماماً مسکونی است")
			break
		}
	}
	var zn Zone
	zb, err := call(ctx, "Regulation", "GetZone", p.Zone)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(zb, &zn); err == nil {
		if zn.HeritageBuffer {
			reasons = append(reasons, "پلاک در حریم میراث فرهنگی است")
		}
		if zn.FaultBuffer {
			reasons = append(reasons, "پلاک در حریم گسل است")
		}
	}
	if p.EngineerID == "" || p.DesignerID == "" {
		reasons = append(reasons, "مهندس ناظر یا طراح تعیین نشده است")
	}
	if len(reasons) > 0 {
		return nil, fmt.Errorf("این پرونده واجد شرایط صدور خودکار نیست و باید از مسیر عادی برود:\n— %s",
			joinLines(reasons))
	}

	// از اینجا مسیر خودکار است: محاسبه، انتظار پرداخت، و اگر تسویه شده، صدور.
	if p.Status == StDraft {
		p.Status = StAppraisal
		if err := putJSON(ctx, mkKey(KeyPermit, permitID), &p); err != nil {
			return nil, err
		}
		if _, err := c.Appraise(ctx, permitID, tariffYear, true); err != nil {
			return nil, err
		}
		if err := mustGetJSON(ctx, mkKey(KeyPermit, permitID), &p, "پروانه"); err != nil {
			return nil, err
		}
	}
	sb, err := call(ctx, "Treasury", "IsSettled", permitID)
	if err != nil {
		return nil, err
	}
	if string(sb) != "true" {
		if err := emit(ctx, "FastTrackPending", permitID,
			fmt.Sprintf("پرونده %s واجد شرایط صدور خودکار است؛ منتظر تسویه %d ریال",
				permitID, p.InvoiceTotal), p.InvoiceTotal); err != nil {
			return nil, err
		}
		return &p, nil
	}
	issued, err := c.Issue(ctx, permitID, 24)
	if err != nil {
		return nil, err
	}
	issued.FastTracked = true
	if err := putJSON(ctx, mkKey(KeyPermit, permitID), issued); err != nil {
		return nil, err
	}
	if err := emit(ctx, "PermitFastTracked", permitID,
		fmt.Sprintf("پروانه %s به‌صورت خودکار و بدون کارشناسی انسانی صادر شد", permitID),
		issued.TotalArea); err != nil {
		return nil, err
	}
	return issued, nil
}

// ---------------------------- بازدید و پایان‌کار ----------------------------

// InspectionResult — خروجی ثبت بازدید.
type InspectionResult struct {
	PermitID     string `json:"permitId"`
	Stage        string `json:"stage"`
	BuiltArea    int64  `json:"builtArea"`
	PermittedArea int64 `json:"permittedArea"`
	DeviationArea int64 `json:"deviationArea"`
	ViolationID   string `json:"violationId,omitempty"`
	Message       string `json:"message"`
}

// ReportInspection ثبت گزارش بازدید مرحله‌ای توسط مهندس ناظر یا کارشناس منطقه.
//
// کشف تخلف اینجا خودکار می‌شود: متراژ گزارش‌شده با متراژ پروانه مقایسه
// می‌شود و اگر انحراف از آستانه بگذرد، پرونده تخلف بی‌درنگ تشکیل و به
// کمیسیون ماده ۱۰۰ ارجاع می‌شود. امروز فاصله بین «ساخته شدن» و «کشف شدن»
// تخلف گاهی سال‌هاست — و در همان فاصله ساختمان تمام و فروخته می‌شود.
func (c *PermitContract) ReportInspection(ctx contractapi.TransactionContextInterface,
	permitID, stage string, builtArea int64, note string) (*InspectionResult, error) {

	if err := requireRole(ctx, RoleEngineer, RoleDistrict, RoleRegulator); err != nil {
		return nil, err
	}
	var p Permit
	if err := mustGetJSON(ctx, mkKey(KeyPermit, permitID), &p, "پروانه"); err != nil {
		return nil, err
	}
	if p.Status != StIssued && p.Status != StFoundation && p.Status != StSkeleton && p.Status != StFinishing {
		return nil, fmt.Errorf("ثبت بازدید در وضعیت «%s» ممکن نیست", p.Status)
	}
	p.BuiltArea = builtArea
	p.UpdatedAt = txTime(ctx)

	res := &InspectionResult{
		PermitID: permitID, Stage: stage,
		BuiltArea: builtArea, PermittedArea: p.TotalArea,
	}

	// آستانه رواداری ۲٪ — خطای اندازه‌گیری و گرد کردن نقشه طبیعی است و نباید
	// هر پرونده‌ای را به کمیسیون بفرستد.
	tolerance := perMille(p.TotalArea, 20)
	dev := builtArea - p.TotalArea
	if dev > tolerance {
		res.DeviationArea = dev
		vid := "VIO-" + permitID
		if _, err := call(ctx, "Violation", "OpenViolation",
			vid, permitID, "EXCESS_AREA", fmt.Sprintf("%d", dev),
			fmt.Sprintf("انحراف %d م² از پروانه در مرحله %s — %s", dev, stage, note)); err != nil {
			return nil, err
		}
		res.ViolationID = vid
		res.Message = fmt.Sprintf("تخلف ساختمانی ثبت شد: %d م² بنای اضافی. پرونده به کمیسیون ماده ۱۰۰ ارجاع شد.", dev)

		from := p.Status
		p.Status = StCommission100
		p.History = append(p.History, Transition{
			From: from, To: StCommission100, By: callerID(ctx), MSP: callerMSP(ctx),
			Note: res.Message, At: p.UpdatedAt, TxID: ctx.GetStub().GetTxID(),
		})
	} else {
		res.Message = fmt.Sprintf("بازدید مرحله %s ثبت شد؛ انحراف در محدوده مجاز است.", stage)
	}

	if err := putJSON(ctx, mkKey(KeyPermit, permitID), &p); err != nil {
		return nil, err
	}
	if err := emit(ctx, "InspectionReported", permitID, res.Message, builtArea); err != nil {
		return nil, err
	}
	return res, nil
}

// IssueCompletion صدور پایان‌کار. توکن‌های قفل‌شده سوزانده می‌شوند: متری که
// ساخته شد دیگر قابل معامله نیست و ظرفیت منطقه به همان اندازه مصرف شده است.
func (c *PermitContract) IssueCompletion(ctx contractapi.TransactionContextInterface,
	permitID, note string) (*Permit, error) {

	if err := requireRole(ctx, RoleRegulator, RoleDistrict); err != nil {
		return nil, err
	}
	var p Permit
	if err := mustGetJSON(ctx, mkKey(KeyPermit, permitID), &p, "پروانه"); err != nil {
		return nil, err
	}
	if p.Status != StFinishing && p.Status != StCommission100 {
		return nil, fmt.Errorf("صدور پایان‌کار از وضعیت «%s» ممکن نیست", p.Status)
	}
	if p.Status == StCommission100 {
		// از مسیر کمیسیون فقط وقتی پایان‌کار می‌گیرد که رأی صادر و جریمه
		// وصول شده باشد.
		vb, err := call(ctx, "Violation", "IsResolved", "VIO-"+permitID)
		if err != nil {
			return nil, err
		}
		if string(vb) != "true" {
			return nil, fmt.Errorf("پرونده تخلف این پروانه هنوز مختومه نشده است")
		}
	}
	sb, err := call(ctx, "Treasury", "IsSettled", permitID)
	if err != nil {
		return nil, err
	}
	if string(sb) != "true" {
		return nil, fmt.Errorf("بدهی این پرونده تسویه نشده است؛ پایان‌کار صادر نمی‌شود")
	}

	// سوزاندن توکن به ترتیب ثابت AllUses.
	for _, use := range AllUses {
		amt := p.TokenLocked[use]
		if amt <= 0 {
			continue
		}
		if _, err := call(ctx, "SqmToken", "BurnFromPermit",
			permitID, p.Region, use, fmt.Sprintf("%d", amt)); err != nil {
			return nil, err
		}
	}

	from := p.Status
	p.Status = StCompletion
	p.UpdatedAt = txTime(ctx)
	p.History = append(p.History, Transition{
		From: from, To: StCompletion, By: callerID(ctx), MSP: callerMSP(ctx),
		Note: note, At: p.UpdatedAt, TxID: ctx.GetStub().GetTxID(),
	})
	if err := putJSON(ctx, mkKey(KeyPermit, permitID), &p); err != nil {
		return nil, err
	}
	if err := emit(ctx, "CompletionIssued", permitID,
		fmt.Sprintf("پایان‌کار پروانه %s صادر شد؛ توکن متراژ سوزانده شد", permitID), p.BuiltArea); err != nil {
		return nil, err
	}
	return &p, nil
}

// CheckExpiry بررسی انقضا. توکن‌های قفل‌شده به مالک برمی‌گردند — او پول
// تراکم را داده و اگر نساخت، دارایی‌اش نباید بسوزد؛ فقط دیگر روی این پروانه
// اعتبار ندارد.
func (c *PermitContract) CheckExpiry(ctx contractapi.TransactionContextInterface,
	permitID string) (*Permit, error) {

	if err := requireRole(ctx, RoleRegulator, RoleDistrict); err != nil {
		return nil, err
	}
	var p Permit
	if err := mustGetJSON(ctx, mkKey(KeyPermit, permitID), &p, "پروانه"); err != nil {
		return nil, err
	}
	now := txTime(ctx)
	if p.Status != StIssued || p.ExpiresAt == 0 || now < p.ExpiresAt {
		return &p, nil
	}
	for _, use := range AllUses {
		amt := p.TokenLocked[use]
		if amt <= 0 {
			continue
		}
		if _, err := call(ctx, "SqmToken", "ReleaseFromPermit",
			permitID, use, p.OwnerID, fmt.Sprintf("%d", amt)); err != nil {
			return nil, err
		}
		p.TokenLocked[use] = 0
	}
	p.Status = StExpired
	p.UpdatedAt = now
	p.History = append(p.History, Transition{
		From: StIssued, To: StExpired, By: callerID(ctx), MSP: callerMSP(ctx),
		Note: "انقضای اعتبار پروانه؛ توکن متراژ آزاد شد",
		At:   now, TxID: ctx.GetStub().GetTxID(),
	})
	if err := putJSON(ctx, mkKey(KeyPermit, permitID), &p); err != nil {
		return nil, err
	}
	if err := emit(ctx, "PermitExpired", permitID,
		fmt.Sprintf("پروانه %s منقضی شد و توکن‌های آن آزاد شدند", permitID), 0); err != nil {
		return nil, err
	}
	return &p, nil
}

// ---------------------------- پرس‌وجو ----------------------------

func (c *PermitContract) ListPermits(ctx contractapi.TransactionContextInterface,
	region, status string) ([]Permit, error) {
	raw, err := queryByPrefix(ctx, KeyPermit)
	if err != nil {
		return nil, err
	}
	out := []Permit{}
	for _, b := range raw {
		var p Permit
		if err := json.Unmarshal(b, &p); err != nil {
			continue
		}
		if region != "" && p.Region != region {
			continue
		}
		if status != "" && p.Status != status {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

func (c *PermitContract) GetHistory(ctx contractapi.TransactionContextInterface,
	permitID string) ([]Transition, error) {
	var p Permit
	if err := mustGetJSON(ctx, mkKey(KeyPermit, permitID), &p, "پروانه"); err != nil {
		return nil, err
	}
	return p.History, nil
}

func joinLines(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += "\n— "
		}
		out += s
	}
	return out
}

func main() {
	cc, err := contractapi.NewChaincode(&PermitContract{})
	if err != nil {
		log.Panicf("خطا در ساخت قرارداد Permit: %v", err)
	}
	if err := cc.Start(); err != nil {
		log.Panicf("خطا در اجرای قرارداد Permit: %v", err)
	}
}
