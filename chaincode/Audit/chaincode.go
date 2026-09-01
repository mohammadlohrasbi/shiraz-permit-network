package main

// ---------------------------------------------------------------------------
// Audit — دفتر شفافیت عمومی
//
// تنها قراردادی است که روی کانال جداست، و علتش دقیقاً همان چیزی است که
// بقیه را روی یک کانال نگه داشت: این قرارداد هیچ‌وقت لازم نیست در تراکنش
// اتمی با هسته باشد. رویدادها پس از قطعی‌شدن، آینه می‌شوند.
//
// جدایی کانال اینجا یک مزیت واقعی می‌دهد: می‌شود عضویت این کانال را وسیع‌تر
// از هسته گرفت — رسانه، شورای شهر، سازمان‌های مردم‌نهاد، دیوان محاسبات —
// بی‌آنکه هیچ‌کدام به دفتر پروانه‌ها و داده مالکان دسترسی پیدا کنند.
// یعنی شفافیت بدون نقض حریم خصوصی، که در یک کانال واحد ممکن نبود.
//
// نکته‌ای درباره صداقت این دفتر: چون آینه است، اگر پرکردنش اختیاری بماند
// دفتری می‌شود که فقط خبرهای خوب را دارد. راه درست این است که آینه‌سازی
// را ناظر لایه API انجام دهد و خودش را در برابر شمارش بلاک هسته پاسخگو
// کند — تابع IntegrityCheck همین کار را می‌کند.
// ---------------------------------------------------------------------------

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/hyperledger/fabric-contract-api-go/contractapi"
)

type AuditContract struct {
	NetworkBase
}

// PublicRecord — یک رویداد در دفتر شفافیت.
type PublicRecord struct {
	DocType    string `json:"docType"`
	Seq        int64  `json:"seq"`
	Kind       string `json:"kind"`
	Subject    string `json:"subject"`
	Region     string `json:"region"`
	Actor      string `json:"actor"` // هش شناسه، نه نام
	MSP        string `json:"msp"`
	Role       string `json:"role"`
	Amount     int64  `json:"amount"`
	Detail     string `json:"detail"`
	SourceTxID string `json:"sourceTxId"` // شناسه تراکنش روی کانال هسته
	At         int64  `json:"at"`
	MirroredAt int64  `json:"mirroredAt"`
}

const seqKey = "AUDIT_SEQ"

func nextSeq(ctx contractapi.TransactionContextInterface) (int64, error) {
	b, err := ctx.GetStub().GetState(seqKey)
	if err != nil {
		return 0, err
	}
	var n int64
	if b != nil {
		n, err = atoi64(string(b))
		if err != nil {
			return 0, err
		}
	}
	n++
	return n, ctx.GetStub().PutState(seqKey, []byte(fmt.Sprintf("%d", n)))
}

// Mirror ثبت یک رویداد در دفتر شفافیت.
//
// SourceTxID کلید یکتاست: اگر ناظر دو بار یک رویداد را بفرستد، دومی رد
// می‌شود. بدون این، هر قطع و وصل ناظر می‌توانست آمار درآمد را دو برابر کند.
func (c *AuditContract) Mirror(ctx contractapi.TransactionContextInterface,
	kind, subject, region, actor, msp, role, detail, sourceTxID string,
	amount, at int64) (*PublicRecord, error) {

	if err := requireRole(ctx, RoleAuditor, RoleRegulator); err != nil {
		return nil, err
	}
	if sourceTxID == "" {
		return nil, fmt.Errorf("شناسه تراکنش مبدأ الزامی است")
	}
	dup, err := ctx.GetStub().GetState(mkKey("AUDITTX", sourceTxID))
	if err != nil {
		return nil, err
	}
	if dup != nil {
		return nil, fmt.Errorf("رویداد تراکنش «%s» قبلاً آینه شده است", sourceTxID)
	}
	seq, err := nextSeq(ctx)
	if err != nil {
		return nil, err
	}
	r := PublicRecord{
		DocType: "public", Seq: seq, Kind: kind, Subject: subject, Region: region,
		Actor: actor, MSP: msp, Role: role, Amount: amount, Detail: detail,
		SourceTxID: sourceTxID, At: at, MirroredAt: txTime(ctx),
	}
	if err := putJSON(ctx, mkKey(KeyAudit, fmt.Sprintf("%012d", seq)), &r); err != nil {
		return nil, err
	}
	if err := ctx.GetStub().PutState(mkKey("AUDITTX", sourceTxID),
		[]byte(fmt.Sprintf("%d", seq))); err != nil {
		return nil, err
	}
	if err := emit(ctx, "PublicRecordAdded", subject, kind, amount); err != nil {
		return nil, err
	}
	return &r, nil
}

// MirrorBatch آینه‌سازی گروهی. ناظر رویدادهای یک بازه را یکجا می‌فرستد.
func (c *AuditContract) MirrorBatch(ctx contractapi.TransactionContextInterface,
	recordsJSON string) (int64, error) {

	if err := requireRole(ctx, RoleAuditor, RoleRegulator); err != nil {
		return 0, err
	}
	var items []PublicRecord
	if err := json.Unmarshal([]byte(recordsJSON), &items); err != nil {
		return 0, fmt.Errorf("فهرست رویدادها نامعتبر است: %v", err)
	}
	count := int64(0)
	for _, it := range items {
		if it.SourceTxID == "" {
			continue
		}
		dup, err := ctx.GetStub().GetState(mkKey("AUDITTX", it.SourceTxID))
		if err != nil {
			return count, err
		}
		if dup != nil {
			continue // تکراری، بی‌صدا رد می‌شود
		}
		seq, err := nextSeq(ctx)
		if err != nil {
			return count, err
		}
		it.DocType = "public"
		it.Seq = seq
		it.MirroredAt = txTime(ctx)
		if err := putJSON(ctx, mkKey(KeyAudit, fmt.Sprintf("%012d", seq)), &it); err != nil {
			return count, err
		}
		if err := ctx.GetStub().PutState(mkKey("AUDITTX", it.SourceTxID),
			[]byte(fmt.Sprintf("%d", seq))); err != nil {
			return count, err
		}
		count++
	}
	if err := emit(ctx, "PublicBatchMirrored", "",
		fmt.Sprintf("%d رویداد در دفتر شفافیت ثبت شد", count), count); err != nil {
		return count, err
	}
	return count, nil
}

// ---------------------------- پرس‌وجوی عمومی ----------------------------

// Feed آخرین رویدادها. بدون کنترل نقش — این دفتر عمومی است.
func (c *AuditContract) Feed(ctx contractapi.TransactionContextInterface,
	kind, region string, limit int64) ([]PublicRecord, error) {

	if limit <= 0 || limit > 500 {
		limit = 100
	}
	raw, err := queryByPrefix(ctx, KeyAudit)
	if err != nil {
		return nil, err
	}
	out := []PublicRecord{}
	// از انتها به ابتدا — کلیدها با شماره ترتیبی صفرپرشده‌اند، پس ترتیب
	// GetStateByRange دقیقاً ترتیب زمانی است.
	for i := len(raw) - 1; i >= 0 && int64(len(out)) < limit; i-- {
		var r PublicRecord
		if err := json.Unmarshal(raw[i], &r); err != nil {
			continue
		}
		if kind != "" && r.Kind != kind {
			continue
		}
		if region != "" && r.Region != region {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// RevenueTransparency جمع درآمد آینه‌شده به تفکیک نوع رویداد و منطقه.
type RevenueRow struct {
	Kind   string `json:"kind"`
	Region string `json:"region"`
	Count  int64  `json:"count"`
	Amount int64  `json:"amount"`
}

func (c *AuditContract) RevenueTransparency(ctx contractapi.TransactionContextInterface,
	region string) ([]RevenueRow, error) {

	raw, err := queryByPrefix(ctx, KeyAudit)
	if err != nil {
		return nil, err
	}
	agg := map[string]*RevenueRow{}
	order := []string{}
	for _, b := range raw {
		var r PublicRecord
		if err := json.Unmarshal(b, &r); err != nil || r.Amount <= 0 {
			continue
		}
		if region != "" && r.Region != region {
			continue
		}
		k := r.Kind + "|" + r.Region
		row, ok := agg[k]
		if !ok {
			row = &RevenueRow{Kind: r.Kind, Region: r.Region}
			agg[k] = row
			order = append(order, k) // ترتیب ورود حفظ می‌شود، پس قطعی است
		}
		row.Count++
		row.Amount += r.Amount
	}
	out := []RevenueRow{}
	for _, k := range order {
		out = append(out, *agg[k])
	}
	return out, nil
}

// IntegrityCheck مقایسه تعداد رویدادهای آینه‌شده با شمارش مورد انتظار.
//
// ناظر لایه API تعداد رویدادهایی را که روی کانال هسته دیده گزارش می‌دهد؛
// این تابع آن را با تعداد ثبت‌شده مقایسه می‌کند. اختلاف یعنی ناظر رویدادی
// را جا انداخته — و همین است که دفتر شفافیت را از یک ویترین به یک ابزار
// حسابرسی تبدیل می‌کند.
type IntegrityReport struct {
	Expected int64 `json:"expected"`
	Recorded int64 `json:"recorded"`
	Gap      int64 `json:"gap"`
	Healthy  bool  `json:"healthy"`
	Note     string `json:"note"`
}

func (c *AuditContract) IntegrityCheck(ctx contractapi.TransactionContextInterface,
	expected int64) (*IntegrityReport, error) {

	b, err := ctx.GetStub().GetState(seqKey)
	if err != nil {
		return nil, err
	}
	var recorded int64
	if b != nil {
		recorded, _ = atoi64(string(b))
	}
	rep := &IntegrityReport{
		Expected: expected, Recorded: recorded,
		Gap: expected - recorded, Healthy: expected == recorded,
	}
	if rep.Healthy {
		rep.Note = "دفتر شفافیت با کانال هسته هم‌خوان است"
	} else if rep.Gap > 0 {
		rep.Note = fmt.Sprintf("%d رویداد از کانال هسته در دفتر شفافیت ثبت نشده است", rep.Gap)
	} else {
		rep.Note = fmt.Sprintf("%d رویداد اضافه در دفتر شفافیت وجود دارد — ناظر را بررسی کنید", -rep.Gap)
	}
	if !rep.Healthy {
		if err := emit(ctx, "IntegrityGapDetected", "",
			rep.Note, rep.Gap); err != nil {
			return nil, err
		}
	}
	return rep, nil
}

func main() {
	cc, err := contractapi.NewChaincode(&AuditContract{})
	if err != nil {
		log.Panicf("خطا در ساخت قرارداد Audit: %v", err)
	}
	if err := cc.Start(); err != nil {
		log.Panicf("خطا در اجرای قرارداد Audit: %v", err)
	}
}
