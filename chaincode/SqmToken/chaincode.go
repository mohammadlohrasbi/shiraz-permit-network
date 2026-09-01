package main

// ---------------------------------------------------------------------------
// SqmToken — توکن متراژ: هر متر مربع، یک توکن
//
// ── چرا توکن، و نه فقط یک عدد در پایگاه‌داده ─────────────────────────────
//
// تراکم امروز یک «مجوز» است: عددی که در یک پرونده نوشته می‌شود و هر بار از
// نو تفسیر می‌گردد. هیچ‌کس در لحظه نمی‌داند در یک منطقه چقدر تراکم فروخته
// شده، چقدر باقی مانده، و چقدر از فروخته‌شده‌ها هرگز ساخته نشده‌اند.
//
// وقتی تراکم توکن می‌شود، چهار چیز به دست می‌آید که هیچ‌کدام با پرونده کاغذی
// یا جدول پایگاه‌داده ممکن نیست:
//
//   ۱) کمیابی قابل اثبات — سقف انتشار هر منطقه در طرح تفصیلی قفل است. کسی
//      نمی‌تواند «کمی بیشتر» بفروشد، حتی با امضای بالاترین مقام. تخطی از
//      سقف اصلاً تراکنش معتبری نیست.
//
//   ۲) موجودی زنده — در هر لحظه معلوم است چقدر منتشر، چقدر قفل (پروانه
//      صادرشده و نساخته)، و چقدر سوخته (ساخته‌شده) است. سه عدد که امروز
//      هیچ سامانه‌ای هم‌زمان ندارد.
//
//   ۳) بازار — تراکمی که در یک پهنه نباید اعمال شود (باغ، بافت تاریخی)
//      قابل انتقال به پهنه‌ای است که ظرفیتش را دارد. مالک باغ به‌جای اینکه
//      برای ساخت، درخت را از بین ببرد، حق توسعه‌اش را می‌فروشد.
//
//   ۴) رد پول — هر متر تراکم به یک پرداخت و یک پرونده وصل است. پرسش
//      «این تراکم از کجا آمد» یک query است، نه یک تحقیق.
//
// توکن‌ها fungible و به تفکیک کاربری‌اند (SQM-RES، SQM-COM، ...) و تبدیل
// بینشان بر پایه ارزش نسبی است — ۱ متر تجاری معادل ۳ متر مسکونی.
// ---------------------------------------------------------------------------

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/hyperledger/fabric-contract-api-go/contractapi"
)

type SqmTokenContract struct {
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

// TokenInfo — معرفی یک نوع توکن.
type TokenInfo struct {
	Symbol        string `json:"symbol"`
	Use           string `json:"use"`
	TitleFa       string `json:"titleFa"`
	Decimals      int64  `json:"decimals"`
	Unit          string `json:"unit"`
	Mintable      bool   `json:"mintable"`
	ValuePerMille int64  `json:"valuePerMille"`
	TotalSupply   int64  `json:"totalSupply"`
}

// TokenInfoAll معرفی همه انواع توکن شبکه.
func (c *SqmTokenContract) TokenInfoAll(ctx contractapi.TransactionContextInterface) ([]TokenInfo, error) {
	out := []TokenInfo{}
	for _, use := range AllUses {
		sup, err := readSupply(ctx, use)
		if err != nil {
			return nil, err
		}
		out = append(out, TokenInfo{
			Symbol: "SQM-" + use, Use: use, TitleFa: UseTitleFa[use],
			Decimals: 0, Unit: "متر مربع",
			Mintable: MintableUse[use], ValuePerMille: UseValuePerMille[use],
			TotalSupply: sup,
		})
	}
	return out, nil
}

// ---------------------------- انتشار ----------------------------

// Mint انتشار توکن در سقف مصوب منطقه. فقط شهرداری.
func (c *SqmTokenContract) Mint(ctx contractapi.TransactionContextInterface,
	region, use, holder string, amount int64, reason string) error {

	if err := requireRole(ctx, RoleRegulator); err != nil {
		return err
	}
	if err := mintTokens(ctx, region, use, holder, amount); err != nil {
		return err
	}
	return emit(ctx, "TokensMinted", mkKey(region, use, holder),
		fmt.Sprintf("انتشار %d توکن %s در منطقه %s برای «%s» — %s",
			amount, "SQM-"+use, region, holder, reason), amount)
}

// MintFromDevelopmentRight انتشار توکن در ازای حق توسعه گواهی‌شده یک پلاک
// فرستنده (باغ یا بافت تاریخی).
//
// این تنها مسیری است که در آن توکنِ کاربری غیرقابل‌انتشار به توکن قابل‌معامله
// تبدیل می‌شود: حق توسعه باغ، معادل ارزشی‌اش در کاربری مقصد منتشر می‌شود.
// نکته: انتشار همچنان در سقف منطقه مقصد است — یعنی این مسیر تراکم کل شهر را
// بالا نمی‌برد، فقط جایش را عوض می‌کند. اگر این قید نبود، TDR به یک در پشتی
// برای تراکم‌فروشی تبدیل می‌شد.
func (c *SqmTokenContract) MintFromDevelopmentRight(ctx contractapi.TransactionContextInterface,
	parcelID, targetRegion, targetUse string, area int64) (int64, error) {

	if err := requireRole(ctx, RoleRegulator); err != nil {
		return 0, err
	}
	db, err := call(ctx, "Parcel", "GetDevelopmentRight", parcelID)
	if err != nil {
		return 0, err
	}
	var dr DevelopmentRight
	if err := json.Unmarshal(db, &dr); err != nil {
		return 0, err
	}
	if !dr.Certified {
		return 0, fmt.Errorf("حق توسعه پلاک «%s» تأیید نشده است", parcelID)
	}
	if dr.Redeemed+area > dr.Area {
		return 0, fmt.Errorf("متراژ درخواستی از باقی‌مانده حق توسعه بیشتر است (کل %d، واگذارشده %d)",
			dr.Area, dr.Redeemed)
	}

	// تبدیل ارزشی: متراژ باغی به معادل کاربری مقصد.
	converted, err := convertUse(area, UseGarden, targetUse)
	if err != nil {
		return 0, err
	}
	if converted <= 0 {
		return 0, fmt.Errorf("متراژ معادل پس از تبدیل صفر شد؛ مقدار ورودی را افزایش دهید")
	}
	if _, err := call(ctx, "Parcel", "RedeemDevelopmentRight",
		parcelID, fmt.Sprintf("%d", area)); err != nil {
		return 0, err
	}
	if err := mintTokens(ctx, targetRegion, targetUse, dr.OwnerID, converted); err != nil {
		return 0, err
	}
	if err := emit(ctx, "TdrMinted", parcelID,
		fmt.Sprintf("%d م² حق توسعه پلاک %s به %d توکن %s در منطقه %s تبدیل شد (مالک %s)",
			area, parcelID, converted, "SQM-"+targetUse, targetRegion, dr.OwnerID), converted); err != nil {
		return 0, err
	}
	return converted, nil
}

// DevelopmentRight — آینه ساختار قرارداد Parcel برای واسازی پاسخ.
type DevelopmentRight struct {
	DocType   string `json:"docType"`
	ParcelID  string `json:"parcelId"`
	OwnerID   string `json:"ownerId"`
	Region    string `json:"region"`
	Area      int64  `json:"area"`
	Certified bool   `json:"certified"`
	Redeemed  int64  `json:"redeemed"`
	Basis     string `json:"basis"`
	CreatedAt int64  `json:"createdAt"`
}

// ---------------------------- انتقال ----------------------------

// Transfer انتقال توکن بین دو دارنده، با کارمزد شهرداری.
//
// کارمزد اینجا فقط منبع درآمد نیست؛ ترمز سفته‌بازی هم هست. اگر انتقال رایگان
// باشد، توکن تراکم به سرعت به یک دارایی سفته‌بازانه تبدیل می‌شود که با ساخت
// و ساز واقعی کاری ندارد.
func (c *SqmTokenContract) Transfer(ctx contractapi.TransactionContextInterface,
	use, from, to string, amount, feePerMille int64, note string) error {

	if err := requireRole(ctx, RoleRegulator, RoleFinance, RoleCitizen, RoleDistrict); err != nil {
		return err
	}
	if amount <= 0 {
		return fmt.Errorf("مقدار انتقال باید مثبت باشد")
	}
	if from == to {
		return fmt.Errorf("مبدأ و مقصد یکسان‌اند")
	}
	fee := int64(0)
	if feePerMille > 0 {
		fee = perMille(amount, feePerMille)
	}
	if err := addBalance(ctx, use, from, -amount); err != nil {
		return err
	}
	if err := addBalance(ctx, use, to, amount-fee); err != nil {
		return err
	}
	if fee > 0 {
		// کارمزد به حساب توکنی شهرداری می‌رود، نه به سوزاندن — تا شهرداری
		// بتواند بعداً همان ظرفیت را در بازار عرضه کند.
		if err := addBalance(ctx, use, "MUNICIPALITY", fee); err != nil {
			return err
		}
	}
	return emit(ctx, "TokensTransferred", mkKey(use, from, to),
		fmt.Sprintf("انتقال %d توکن %s از «%s» به «%s» (کارمزد %d) — %s",
			amount, "SQM-"+use, from, to, fee, note), amount)
}

// Convert تبدیل توکن یک کاربری به کاربری دیگر بر پایه ارزش نسبی.
func (c *SqmTokenContract) Convert(ctx contractapi.TransactionContextInterface,
	holder, fromUse, toUse string, amount int64) (int64, error) {

	if err := requireRole(ctx, RoleRegulator, RoleFinance); err != nil {
		return 0, err
	}
	converted, err := convertUse(amount, fromUse, toUse)
	if err != nil {
		return 0, err
	}
	if converted <= 0 {
		return 0, fmt.Errorf("مقدار معادل پس از تبدیل صفر شد")
	}
	if !MintableUse[toUse] {
		return 0, fmt.Errorf("تبدیل به کاربری «%s» ممکن نیست", UseTitleFa[toUse])
	}
	if err := addBalance(ctx, fromUse, holder, -amount); err != nil {
		return 0, err
	}
	if err := addBalance(ctx, toUse, holder, converted); err != nil {
		return 0, err
	}
	if err := addSupply(ctx, fromUse, -amount); err != nil {
		return 0, err
	}
	if err := addSupply(ctx, toUse, converted); err != nil {
		return 0, err
	}
	if err := emit(ctx, "TokensConverted", holder,
		fmt.Sprintf("تبدیل %d توکن %s به %d توکن %s برای «%s»",
			amount, UseTitleFa[fromUse], converted, UseTitleFa[toUse], holder), converted); err != nil {
		return 0, err
	}
	return converted, nil
}

// ---------------------------- وثیقه پروانه ----------------------------

// LockForPermit قفل توکن در وثیقه یک پروانه. از قرارداد Permit صدا زده می‌شود.
func (c *SqmTokenContract) LockForPermit(ctx contractapi.TransactionContextInterface,
	permitID, use, holder string, amount int64) error {

	if err := lockTokens(ctx, permitID, use, holder, amount); err != nil {
		return err
	}
	return emit(ctx, "TokensLocked", permitID,
		fmt.Sprintf("قفل %d توکن %s برای پروانه %s (دارنده %s)",
			amount, "SQM-"+use, permitID, holder), amount)
}

// BurnFromPermit سوزاندن توکن قفل‌شده هنگام پایان‌کار.
func (c *SqmTokenContract) BurnFromPermit(ctx contractapi.TransactionContextInterface,
	permitID, region, use string, amount int64) error {

	if err := burnEscrow(ctx, permitID, region, use, amount); err != nil {
		return err
	}
	return emit(ctx, "TokensBurned", permitID,
		fmt.Sprintf("سوزاندن %d توکن %s پروانه %s — ظرفیت منطقه %s مصرف شد",
			amount, "SQM-"+use, permitID, region), amount)
}

// ReleaseFromPermit آزادسازی توکن قفل‌شده هنگام انقضا یا ابطال.
func (c *SqmTokenContract) ReleaseFromPermit(ctx contractapi.TransactionContextInterface,
	permitID, use, holder string, amount int64) error {

	if err := releaseEscrow(ctx, permitID, use, holder, amount); err != nil {
		return err
	}
	return emit(ctx, "TokensReleased", permitID,
		fmt.Sprintf("آزادسازی %d توکن %s پروانه %s به «%s»",
			amount, "SQM-"+use, permitID, holder), amount)
}

// ---------------------------- پرس‌وجو ----------------------------

func (c *SqmTokenContract) BalanceOf(ctx contractapi.TransactionContextInterface,
	use, holder string) (int64, error) {
	return readBalance(ctx, use, holder)
}

// Holding — سبد توکن یک دارنده.
type Holding struct {
	Holder    string           `json:"holder"`
	Balances  map[string]int64 `json:"balances"`
	TotalResEquivalent int64   `json:"totalResEquivalent"` // ارزش کل معادل مسکونی
}

// PortfolioOf سبد کامل یک دارنده. ارزش کل به معادل مسکونی بیان می‌شود تا
// دارایی‌های نامتجانس قابل مقایسه باشند.
func (c *SqmTokenContract) PortfolioOf(ctx contractapi.TransactionContextInterface,
	holder string) (*Holding, error) {
	h := &Holding{Holder: holder, Balances: map[string]int64{}}
	for _, use := range AllUses {
		b, err := readBalance(ctx, use, holder)
		if err != nil {
			return nil, err
		}
		if b > 0 {
			h.Balances["SQM-"+use] = b
			h.TotalResEquivalent += perMille(b, UseValuePerMille[use])
		}
	}
	return h, nil
}

// EscrowOf توکن قفل‌شده یک پروانه.
func (c *SqmTokenContract) EscrowOf(ctx contractapi.TransactionContextInterface,
	permitID string) (map[string]int64, error) {
	out := map[string]int64{}
	for _, use := range AllUses {
		v, err := readEscrow(ctx, permitID, use)
		if err != nil {
			return nil, err
		}
		if v > 0 {
			out["SQM-"+use] = v
		}
	}
	return out, nil
}

// CapacityReport وضعیت ظرفیت تراکم شهر — منتشر، قفل، سوخته، باقی‌مانده.
//
// این گزارش چیزی را نشان می‌دهد که امروز هیچ‌جا در دسترس نیست: چه مقدار
// تراکم فروخته شده ولی هرگز ساخته نشده. آن عدد هم بدهی پنهان شهر به
// زیرساخت است و هم اهرم برنامه‌ریزی.
type CapacityRow struct {
	Region    string `json:"region"`
	Use       string `json:"use"`
	TitleFa   string `json:"titleFa"`
	Ceiling   int64  `json:"ceiling"`
	Minted    int64  `json:"minted"`
	Burned    int64  `json:"burned"`
	Remaining int64  `json:"remaining"`
	Outstanding int64 `json:"outstanding"` // منتشرشده ولی نساخته
	UtilPerMille int64 `json:"utilPerMille"`
}

func (c *SqmTokenContract) CapacityReport(ctx contractapi.TransactionContextInterface,
	region string) ([]CapacityRow, error) {
	raw, err := queryByPrefix(ctx, KeyQuota)
	if err != nil {
		return nil, err
	}
	out := []CapacityRow{}
	for _, b := range raw {
		var q Quota
		if err := json.Unmarshal(b, &q); err != nil {
			continue
		}
		if region != "" && q.Region != region {
			continue
		}
		out = append(out, CapacityRow{
			Region: q.Region, Use: q.Use, TitleFa: UseTitleFa[q.Use],
			Ceiling: q.Ceiling, Minted: q.Minted, Burned: q.Burned,
			Remaining:   q.Ceiling - q.Minted,
			Outstanding: q.Minted - q.Burned,
			UtilPerMille: ratioPerMille(q.Minted, q.Ceiling),
		})
	}
	return out, nil
}

func main() {
	cc, err := contractapi.NewChaincode(&SqmTokenContract{})
	if err != nil {
		log.Panicf("خطا در ساخت قرارداد SqmToken: %v", err)
	}
	if err := cc.Start(); err != nil {
		log.Panicf("خطا در اجرای قرارداد SqmToken: %v", err)
	}
}
