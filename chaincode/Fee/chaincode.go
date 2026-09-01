package main

// ---------------------------------------------------------------------------
// Fee — موتور محاسبه عوارض
//
// این قرارداد داده نمی‌سازد؛ فقط داده‌های سه قرارداد دیگر (پروانه، پلاک،
// مقررات) را می‌گیرد، فرمول‌های دفترچه تعرفه را روی آنها اجرا می‌کند و
// صورت‌حسابی با شرح مبنای هر ردیف تولید می‌کند.
//
// جدا نگه داشتنش از Permit عمدی است: فرمول عوارض چیزی است که هر سال عوض
// می‌شود و باید بتوان بازمحاسبه‌اش کرد بی‌آنکه ماشین حالت پروانه دست بخورد.
// و از همه مهم‌تر، Recalculate اجازه می‌دهد بازرس هر صورت‌حساب صادرشده را
// روی همان داده تاریخی از نو بسازد و با عدد ثبت‌شده مقایسه کند — چیزی که
// در سامانه‌های امروزی عملاً ناممکن است.
// ---------------------------------------------------------------------------

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/hyperledger/fabric-contract-api-go/contractapi"
)

type FeeContract struct {
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

// gather داده‌های لازم برای محاسبه را از سه قرارداد جمع می‌کند.
func gather(ctx contractapi.TransactionContextInterface,
	permitID string, tariffYear int64) (*FeeInput, error) {

	var in FeeInput

	pb, err := call(ctx, "Permit", "GetPermit", permitID)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(pb, &in.Permit); err != nil {
		return nil, fmt.Errorf("خطا در خواندن پروانه: %v", err)
	}

	cb, err := call(ctx, "Parcel", "GetParcel", in.Permit.ParcelID)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(cb, &in.Parcel); err != nil {
		return nil, fmt.Errorf("خطا در خواندن پلاک: %v", err)
	}

	rb, err := call(ctx, "Regulation", "GetRegion", in.Permit.Region)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(rb, &in.Region); err != nil {
		return nil, fmt.Errorf("خطا در خواندن منطقه: %v", err)
	}

	zb, err := call(ctx, "Regulation", "GetZone", in.Permit.Zone)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(zb, &in.Zone); err != nil {
		return nil, fmt.Errorf("خطا در خواندن پهنه: %v", err)
	}

	tb, err := call(ctx, "Regulation", "GetTariff", fmt.Sprintf("%d", tariffYear))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(tb, &in.Tariff); err != nil {
		return nil, fmt.Errorf("خطا در خواندن دفترچه تعرفه: %v", err)
	}
	return &in, nil
}

// BuildInvoice ساخت و ثبت صورت‌حساب عوارض.
func (c *FeeContract) BuildInvoice(ctx contractapi.TransactionContextInterface,
	permitID string, tariffYear int64, cashPay bool) (*Invoice, error) {

	if err := requireRole(ctx, RoleFinance, RoleRegulator, RoleDistrict); err != nil {
		return nil, err
	}
	in, err := gather(ctx, permitID, tariffYear)
	if err != nil {
		return nil, err
	}
	in.CashPay = cashPay

	now := txTime(ctx)
	inv := computeFees(*in, now)

	// صورت‌حساب قبلی اگر بوده و پرداختی داشته، پرداختی‌اش منتقل می‌شود.
	var prev Invoice
	ok, err := getJSON(ctx, mkKey(KeyInvoice, permitID), &prev)
	if err != nil {
		return nil, err
	}
	if ok {
		if prev.Settled {
			return nil, fmt.Errorf("صورت‌حساب این پروانه تسویه شده و قابل بازمحاسبه نیست")
		}
		inv.Paid = prev.Paid
		inv.Penalty = prev.Penalty
		inv.Installments = prev.Installments
		inv.CreatedAt = prev.CreatedAt
	}
	inv.Settled = inv.Paid >= inv.Total+inv.Penalty

	if err := putJSON(ctx, mkKey(KeyInvoice, permitID), &inv); err != nil {
		return nil, err
	}
	if err := emit(ctx, "InvoiceBuilt", permitID,
		fmt.Sprintf("صورت‌حساب پروانه %s: جمع %d، تخفیف %d، قابل پرداخت %d ریال",
			permitID, inv.Subtotal, inv.Discount, inv.Total), inv.Total); err != nil {
		return nil, err
	}
	return &inv, nil
}

// Recalculate بازمحاسبه بدون ثبت — ابزار بازرس و ابزار شهروند.
//
// شهروند می‌تواند قبل از هر اقدامی ببیند سناریوی مورد نظرش چقدر عوارض دارد،
// و بازرس می‌تواند صورت‌حساب صادرشده را با همین تابع بازتولید کند. اگر عدد
// فرق کرد، یا داده‌ای بعداً عوض شده یا صورت‌حساب دستکاری شده — و هر دو
// یافته‌اند.
func (c *FeeContract) Recalculate(ctx contractapi.TransactionContextInterface,
	permitID string, tariffYear int64, cashPay bool) (*Invoice, error) {

	in, err := gather(ctx, permitID, tariffYear)
	if err != nil {
		return nil, err
	}
	in.CashPay = cashPay
	inv := computeFees(*in, txTime(ctx))
	return &inv, nil
}

// EstimateNew برآورد عوارض یک سناریوی فرضی، بدون نیاز به پرونده.
//
// این تابع بیشترین اثر را روی «تسهیل فرآیند» دارد: سرمایه‌گذار می‌تواند پیش
// از خرید زمین بداند ساخت روی آن چقدر عوارض دارد. امروز این عدد فقط پس از
// تشکیل پرونده معلوم می‌شود، و همین ابهام بخشی از ریسکی است که به قیمت مسکن
// منتقل می‌شود.
func (c *FeeContract) EstimateNew(ctx contractapi.TransactionContextInterface,
	region, zoneCode string, landArea int64, floorsJSON string,
	parkingProvided int64, tariffYear int64, wornTexture, greenCertified, cashPay bool) (*Invoice, error) {

	var in FeeInput

	rb, err := call(ctx, "Regulation", "GetRegion", region)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(rb, &in.Region); err != nil {
		return nil, err
	}
	zb, err := call(ctx, "Regulation", "GetZone", zoneCode)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(zb, &in.Zone); err != nil {
		return nil, err
	}
	tb, err := call(ctx, "Regulation", "GetTariff", fmt.Sprintf("%d", tariffYear))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(tb, &in.Tariff); err != nil {
		return nil, err
	}

	var floors []FloorSpec
	if err := json.Unmarshal([]byte(floorsJSON), &floors); err != nil {
		return nil, fmt.Errorf("مشخصات طبقات نامعتبر است: %v", err)
	}
	total, required := int64(0), int64(0)
	for _, f := range floors {
		total += f.Area
		if f.Use == UseCommercial && f.Area >= 50 {
			required += f.Units * 2
		} else {
			required += f.Units
		}
	}
	in.Parcel = Parcel{
		DocType: "parcel", Region: region, Zone: zoneCode,
		LandArea: landArea, WornTexture: wornTexture,
	}
	in.Permit = Permit{
		DocType: "permit", ID: "ESTIMATE", Region: region, Zone: zoneCode,
		Floors: floors, TotalArea: total,
		AllowedArea:     perMille(landArea, in.Zone.FarPerMille),
		ParkingRequired: required, ParkingProvided: parkingProvided,
		GreenCertified:  greenCertified,
	}
	if in.Permit.TotalArea > in.Permit.AllowedArea {
		in.Permit.ExcessArea = in.Permit.TotalArea - in.Permit.AllowedArea
	}
	in.CashPay = cashPay
	inv := computeFees(in, txTime(ctx))
	return &inv, nil
}

func (c *FeeContract) GetInvoice(ctx contractapi.TransactionContextInterface,
	permitID string) (*Invoice, error) {
	var inv Invoice
	if err := mustGetJSON(ctx, mkKey(KeyInvoice, permitID), &inv, "صورت‌حساب"); err != nil {
		return nil, err
	}
	return &inv, nil
}

// AddLine افزودن یک ردیف دستی به صورت‌حساب (عوارض تأخیر، بهای خدمات خاص،
// جریمه کمیسیون). هر ردیف دستی رویداد جدا می‌سازد تا در حسابرسی برجسته باشد.
func (c *FeeContract) AddLine(ctx contractapi.TransactionContextInterface,
	permitID, code, title, basis string, amount int64) (*Invoice, error) {

	if err := requireRole(ctx, RoleFinance, RoleRegulator, RoleCommission); err != nil {
		return nil, err
	}
	var inv Invoice
	if err := mustGetJSON(ctx, mkKey(KeyInvoice, permitID), &inv, "صورت‌حساب"); err != nil {
		return nil, err
	}
	if inv.Settled {
		return nil, fmt.Errorf("صورت‌حساب تسویه شده است؛ افزودن ردیف ممکن نیست")
	}
	inv.Lines = append(inv.Lines, FeeLine{Code: code, Title: title, Basis: basis, Amount: amount})
	inv.Subtotal += amount
	inv.Total += amount
	inv.UpdatedAt = txTime(ctx)
	inv.Settled = inv.Paid >= inv.Total+inv.Penalty
	if err := putJSON(ctx, mkKey(KeyInvoice, permitID), &inv); err != nil {
		return nil, err
	}
	if err := emit(ctx, "InvoiceLineAdded", permitID,
		fmt.Sprintf("ردیف دستی «%s» به مبلغ %d ریال — %s", title, amount, basis), amount); err != nil {
		return nil, err
	}
	return &inv, nil
}

// ApplyDelayFee عوارض تأخیر در اتمام عملیات ساختمانی.
// خودکار از فاصله زمان جاری تا انقضای پروانه حساب می‌شود.
func (c *FeeContract) ApplyDelayFee(ctx contractapi.TransactionContextInterface,
	permitID string, tariffYear int64) (*Invoice, error) {

	if err := requireRole(ctx, RoleFinance, RoleRegulator, RoleDistrict); err != nil {
		return nil, err
	}
	in, err := gather(ctx, permitID, tariffYear)
	if err != nil {
		return nil, err
	}
	now := txTime(ctx)
	if in.Permit.ExpiresAt == 0 || now <= in.Permit.ExpiresAt {
		return nil, fmt.Errorf("پروانه هنوز منقضی نشده است؛ عوارض تأخیر تعلق نمی‌گیرد")
	}
	years := (now - in.Permit.ExpiresAt) / SecondsPerYear
	if years < 1 {
		years = 1
	}
	p := in.Region.PriceZonal[UseResidential]
	amount := perMille(in.Permit.TotalArea*p, in.Tariff.DelayFeePerMilleYearly) * years

	return c.AddLine(ctx, permitID, "DELAY", "عوارض تأخیر در اتمام عملیات ساختمانی",
		fmt.Sprintf("%d م² × %d ریال × %d‰ × %d سال",
			in.Permit.TotalArea, p, in.Tariff.DelayFeePerMilleYearly, years), amount)
}

func main() {
	cc, err := contractapi.NewChaincode(&FeeContract{})
	if err != nil {
		log.Panicf("خطا در ساخت قرارداد Fee: %v", err)
	}
	if err := cc.Start(); err != nil {
		log.Panicf("خطا در اجرای قرارداد Fee: %v", err)
	}
}
