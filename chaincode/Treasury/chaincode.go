package main

// ---------------------------------------------------------------------------
// Treasury — خزانه: پرداخت، اقساط، جریمه دیرکرد و دفتر درآمد
//
// دو تصمیم طراحی که شاید در نگاه اول عجیب باشند:
//
// ۱) دفتر درآمد به تفکیک سرفصل و سال نگه داشته می‌شود، نه فقط جمع کل. علت:
//    سؤال واقعی مدیر شهری «چقدر وصول شد» نیست، «از کجا وصول شد» است. وقتی
//    ۷۰٪ درآمد یک منطقه از فروش تراکم مازاد بیاید، آن عدد خودش هشدار است —
//    ولی فقط اگر تفکیک شده باشد.
//
// ۲) پرداخت از سوی شهروند «ثبت» نمی‌شود؛ تأیید بانک عامل ثبت می‌شود. شهروند
//    به درگاه پرداخت می‌رود و سازمان مالی نتیجه را روی زنجیره می‌آورد. اگر
//    شهروند می‌توانست خودش پرداخت را ثبت کند، زنجیره فقط ادعای او را ضبط
//    می‌کرد، نه واقعیت را.
// ---------------------------------------------------------------------------

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/hyperledger/fabric-contract-api-go/contractapi"
)

type TreasuryContract struct {
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

// Receipt — رسید پرداخت.
type Receipt struct {
	DocType   string `json:"docType"`
	ID        string `json:"id"`
	PermitID  string `json:"permitId"`
	Amount    int64  `json:"amount"`
	Channel   string `json:"channel"` // درگاه، شعبه، پایا
	RefNo     string `json:"refNo"`   // شماره پیگیری بانکی
	Category  string `json:"category"`
	PaidAt    int64  `json:"paidAt"`
	Confirmer string `json:"confirmer"`
}

// RevenueBucket — سرفصل درآمد یک سال.
type RevenueBucket struct {
	DocType string `json:"docType"`
	Year    int64  `json:"year"`
	Code    string `json:"code"`
	Title   string `json:"title"`
	Amount  int64  `json:"amount"`
	Count   int64  `json:"count"`
}

// ---------------------------- اقساط ----------------------------

// ScheduleInstallments زمان‌بندی اقساط.
//
// اقساط ابزار وصول است نه ارفاق: بدهی که یکجا قابل پرداخت نباشد اغلب اصلاً
// وصول نمی‌شود و پرونده سال‌ها معلق می‌ماند. با پیش‌پرداخت و اقساط، پرونده
// جلو می‌رود و شهرداری جریان نقدی می‌گیرد.
func (c *TreasuryContract) ScheduleInstallments(ctx contractapi.TransactionContextInterface,
	permitID string, count, downPaymentPerMille, intervalDays int64) (*Invoice, error) {

	if err := requireRole(ctx, RoleFinance, RoleRegulator); err != nil {
		return nil, err
	}
	if count < 1 || count > 36 {
		return nil, fmt.Errorf("تعداد اقساط باید بین ۱ تا ۳۶ باشد")
	}
	ib, err := call(ctx, "Fee", "GetInvoice", permitID)
	if err != nil {
		return nil, err
	}
	var inv Invoice
	if err := json.Unmarshal(ib, &inv); err != nil {
		return nil, err
	}
	if inv.Settled {
		return nil, fmt.Errorf("این صورت‌حساب تسویه شده است")
	}
	if inv.Paid > 0 {
		return nil, fmt.Errorf("برای صورت‌حسابی که پرداخت جزئی دارد نمی‌توان جدول اقساط جدید ساخت")
	}

	now := txTime(ctx)
	if intervalDays <= 0 {
		intervalDays = 30
	}
	down := perMille(inv.Total, downPaymentPerMille)
	rest := inv.Total - down
	each := rest / count
	// باقی‌مانده تقسیم به قسط آخر می‌رود تا جمع اقساط دقیقاً برابر کل باشد.
	last := rest - each*(count-1)

	sched := []Installment{}
	no := int64(1)
	if down > 0 {
		sched = append(sched, Installment{No: no, DueAt: now, Amount: down})
		no++
	}
	for i := int64(0); i < count; i++ {
		amt := each
		if i == count-1 {
			amt = last
		}
		sched = append(sched, Installment{
			No: no, DueAt: now + (i+1)*intervalDays*SecondsPerDay, Amount: amt,
		})
		no++
	}
	inv.Installments = sched
	inv.UpdatedAt = now
	if err := putJSON(ctx, mkKey(KeyInvoice, permitID), &inv); err != nil {
		return nil, err
	}

	// آینه جزئیات مالی در مجموعه خصوصی — فقط سازمان مالی و بازرسی.
	if enc, e := json.Marshal(sched); e == nil {
		_ = ctx.GetStub().PutPrivateData(PDCFinance, mkKey("SCHED", permitID), enc)
	}

	if err := emit(ctx, "InstallmentsScheduled", permitID,
		fmt.Sprintf("جدول %d قسطی برای پروانه %s (پیش‌پرداخت %d ریال)",
			count, permitID, down), inv.Total); err != nil {
		return nil, err
	}
	return &inv, nil
}

// ---------------------------- پرداخت ----------------------------

// ConfirmPayment ثبت پرداخت تأییدشده توسط بانک عامل.
func (c *TreasuryContract) ConfirmPayment(ctx contractapi.TransactionContextInterface,
	receiptID, permitID string, amount int64, channel, refNo, category string) (*Invoice, error) {

	if err := requireRole(ctx, RoleFinance); err != nil {
		return nil, err
	}
	if amount <= 0 {
		return nil, fmt.Errorf("مبلغ پرداخت باید مثبت باشد")
	}
	dup, err := ctx.GetStub().GetState(mkKey(KeyReceipt, receiptID))
	if err != nil {
		return nil, err
	}
	if dup != nil {
		return nil, fmt.Errorf("رسید «%s» قبلاً ثبت شده است", receiptID)
	}

	ib, err := call(ctx, "Fee", "GetInvoice", permitID)
	if err != nil {
		return nil, err
	}
	var inv Invoice
	if err := json.Unmarshal(ib, &inv); err != nil {
		return nil, err
	}

	now := txTime(ctx)
	inv.Penalty = computePenalty(inv, now)
	due := inv.Total + inv.Penalty
	if inv.Paid+amount > due {
		return nil, fmt.Errorf("مبلغ پرداختی از مانده بدهی بیشتر است (مانده %d ریال)", due-inv.Paid)
	}
	inv.Paid += amount

	// تخصیص پرداخت به اقساط، به ترتیب سررسید.
	rem := amount
	for i := range inv.Installments {
		if rem <= 0 {
			break
		}
		gap := inv.Installments[i].Amount - inv.Installments[i].Paid
		if gap <= 0 {
			continue
		}
		pay := gap
		if pay > rem {
			pay = rem
		}
		inv.Installments[i].Paid += pay
		rem -= pay
	}
	inv.Settled = inv.Paid >= due
	inv.UpdatedAt = now
	if err := putJSON(ctx, mkKey(KeyInvoice, permitID), &inv); err != nil {
		return nil, err
	}

	if category == "" {
		category = "PERMIT_FEE"
	}
	rcp := Receipt{
		DocType: "receipt", ID: receiptID, PermitID: permitID, Amount: amount,
		Channel: channel, RefNo: refNo, Category: category,
		PaidAt: now, Confirmer: callerID(ctx),
	}
	if err := putJSON(ctx, mkKey(KeyReceipt, receiptID), &rcp); err != nil {
		return nil, err
	}
	if err := bookRevenue(ctx, category, amount, now); err != nil {
		return nil, err
	}
	if err := emit(ctx, "PaymentConfirmed", permitID,
		fmt.Sprintf("پرداخت %d ریال برای پروانه %s (رسید %s، پیگیری %s) — مانده %d ریال",
			amount, permitID, receiptID, refNo, due-inv.Paid), amount); err != nil {
		return nil, err
	}
	if inv.Settled {
		if err := emit(ctx, "InvoiceSettled", permitID,
			fmt.Sprintf("صورت‌حساب پروانه %s تسویه شد (%d ریال)", permitID, inv.Paid), inv.Paid); err != nil {
			return nil, err
		}
	}
	return &inv, nil
}

// computePenalty جریمه دیرکرد اقساط معوق.
//
// روی «مانده هر قسط سررسیدشده» حساب می‌شود، نه روی کل بدهی. اگر روی کل
// بدهی باشد، کسی که منظم قسط می‌دهد هم جریمه می‌شود، و آن‌وقت انگیزه نظم
// از بین می‌رود.
func computePenalty(inv Invoice, now int64) int64 {
	total := int64(0)
	for _, ins := range inv.Installments {
		gap := ins.Amount - ins.Paid
		if gap <= 0 || now <= ins.DueAt {
			continue
		}
		months := (now - ins.DueAt) / SecondsPerMonth
		if months < 1 {
			continue
		}
		// ضریب ۲۰ در هزار ماهانه؛ در نسخه واقعی از دفترچه تعرفه خوانده شود.
		total += perMille(gap, 20) * months
	}
	return total
}

// AccruePenalty به‌روزرسانی جریمه دیرکرد روی صورت‌حساب.
// این تابع را زمان‌بند لایه API ماهانه صدا می‌زند — زنجیره خودش تایمر ندارد.
func (c *TreasuryContract) AccruePenalty(ctx contractapi.TransactionContextInterface,
	permitID string) (*Invoice, error) {

	if err := requireRole(ctx, RoleFinance, RoleRegulator); err != nil {
		return nil, err
	}
	var inv Invoice
	if err := mustGetJSON(ctx, mkKey(KeyInvoice, permitID), &inv, "صورت‌حساب"); err != nil {
		return nil, err
	}
	now := txTime(ctx)
	old := inv.Penalty
	inv.Penalty = computePenalty(inv, now)
	if inv.Penalty == old {
		return &inv, nil
	}
	inv.Settled = inv.Paid >= inv.Total+inv.Penalty
	inv.UpdatedAt = now
	if err := putJSON(ctx, mkKey(KeyInvoice, permitID), &inv); err != nil {
		return nil, err
	}
	if err := emit(ctx, "PenaltyAccrued", permitID,
		fmt.Sprintf("جریمه دیرکرد پروانه %s از %d به %d ریال رسید", permitID, old, inv.Penalty),
		inv.Penalty-old); err != nil {
		return nil, err
	}
	return &inv, nil
}

// IsSettled — شرط صدور پروانه و پایان‌کار. رشته "true"/"false" برمی‌گرداند
// چون از InvokeChaincode مصرف می‌شود و آنجا payload خام است.
func (c *TreasuryContract) IsSettled(ctx contractapi.TransactionContextInterface,
	permitID string) (string, error) {
	var inv Invoice
	ok, err := getJSON(ctx, mkKey(KeyInvoice, permitID), &inv)
	if err != nil {
		return "false", err
	}
	if !ok {
		return "false", nil
	}
	if inv.Settled {
		return "true", nil
	}
	return "false", nil
}

func (c *TreasuryContract) GetReceipt(ctx contractapi.TransactionContextInterface,
	receiptID string) (*Receipt, error) {
	var r Receipt
	if err := mustGetJSON(ctx, mkKey(KeyReceipt, receiptID), &r, "رسید"); err != nil {
		return nil, err
	}
	return &r, nil
}

// ---------------------------- دفتر درآمد ----------------------------

var revenueTitle = map[string]string{
	"PERMIT_FEE":  "عوارض صدور پروانه",
	"EXCESS_FEE":  "عوارض تراکم مازاد",
	"TDR_FEE":     "کارمزد انتقال حق توسعه",
	"TDR_SALE":    "فروش توکن تراکم توسط شهرداری",
	"PENALTY_100": "جریمه کمیسیون ماده ۱۰۰",
	"LATE_FEE":    "جریمه دیرکرد",
	"NOPARK_FEE":  "عوارض کسری پارکینگ",
	"DELAY_FEE":   "عوارض تأخیر در اتمام",
	"OTHER":       "سایر",
}

// bookRevenue ثبت در دفتر درآمد. سال از زمان تراکنش به‌صورت میلادی تقریبی
// گرفته می‌شود؛ تبدیل به شمسی کار لایه نمایش است تا زنجیره قطعی بماند.
func bookRevenue(ctx contractapi.TransactionContextInterface,
	code string, amount, now int64) error {

	year := 1970 + now/SecondsPerYear
	key := mkKey(KeyRevenue, fmt.Sprintf("%d", year), code)
	var b RevenueBucket
	ok, err := getJSON(ctx, key, &b)
	if err != nil {
		return err
	}
	if !ok {
		title := revenueTitle[code]
		if title == "" {
			title = code
		}
		b = RevenueBucket{DocType: "revenue", Year: year, Code: code, Title: title}
	}
	b.Amount += amount
	b.Count++
	return putJSON(ctx, key, &b)
}

// BookExternalRevenue ثبت درآمدی که از خارج مسیر پروانه می‌آید — مثلاً
// کارمزد بازار تراکم یا فروش مستقیم توکن.
func (c *TreasuryContract) BookExternalRevenue(ctx contractapi.TransactionContextInterface,
	code string, amount int64, note string) error {

	if err := requireRole(ctx, RoleFinance, RoleRegulator); err != nil {
		return err
	}
	if amount <= 0 {
		return fmt.Errorf("مبلغ باید مثبت باشد")
	}
	if err := bookRevenue(ctx, code, amount, txTime(ctx)); err != nil {
		return err
	}
	return emit(ctx, "ExternalRevenueBooked", code,
		fmt.Sprintf("%s: %d ریال — %s", revenueTitle[code], amount, note), amount)
}

// RevenueReport گزارش درآمد. هرکسی می‌تواند بخواند — همین شفافیت است.
func (c *TreasuryContract) RevenueReport(ctx contractapi.TransactionContextInterface,
	year int64) ([]RevenueBucket, error) {
	raw, err := queryByPrefix(ctx, KeyRevenue)
	if err != nil {
		return nil, err
	}
	out := []RevenueBucket{}
	for _, b := range raw {
		var rb RevenueBucket
		if err := json.Unmarshal(b, &rb); err == nil && (year == 0 || rb.Year == year) {
			out = append(out, rb)
		}
	}
	return out, nil
}

// OutstandingReport فهرست پرونده‌های دارای بدهی معوق — ابزار وصول مطالبات.
type Outstanding struct {
	PermitID  string `json:"permitId"`
	Total     int64  `json:"total"`
	Paid      int64  `json:"paid"`
	Penalty   int64  `json:"penalty"`
	Remaining int64  `json:"remaining"`
	OverdueNo int64  `json:"overdueInstallments"`
}

func (c *TreasuryContract) OutstandingReport(ctx contractapi.TransactionContextInterface) ([]Outstanding, error) {
	if err := requireRole(ctx, RoleFinance, RoleRegulator, RoleAuditor); err != nil {
		return nil, err
	}
	raw, err := queryByPrefix(ctx, KeyInvoice)
	if err != nil {
		return nil, err
	}
	now := txTime(ctx)
	out := []Outstanding{}
	for _, b := range raw {
		var inv Invoice
		if err := json.Unmarshal(b, &inv); err != nil || inv.Settled {
			continue
		}
		pen := computePenalty(inv, now)
		overdue := int64(0)
		for _, ins := range inv.Installments {
			if ins.Amount > ins.Paid && now > ins.DueAt {
				overdue++
			}
		}
		out = append(out, Outstanding{
			PermitID: inv.PermitID, Total: inv.Total, Paid: inv.Paid,
			Penalty: pen, Remaining: inv.Total + pen - inv.Paid, OverdueNo: overdue,
		})
	}
	return out, nil
}

func main() {
	cc, err := contractapi.NewChaincode(&TreasuryContract{})
	if err != nil {
		log.Panicf("خطا در ساخت قرارداد Treasury: %v", err)
	}
	if err := cc.Start(); err != nil {
		log.Panicf("خطا در اجرای قرارداد Treasury: %v", err)
	}
}
