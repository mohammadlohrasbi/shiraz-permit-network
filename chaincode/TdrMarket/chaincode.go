package main

// ---------------------------------------------------------------------------
// TdrMarket — بازار انتقال حق توسعه
//
// این قرارداد جایی است که «کسب درآمد بیشتر برای شهرداری» و «حفظ باغات شیراز»
// به یک سازوکار واحد تبدیل می‌شوند.
//
// ── مسئله ────────────────────────────────────────────────────────────────
// مالک باغی در قصردشت اجازه ساخت ندارد. زمینش گران است ولی بی‌بازده. عملاً
// دو راه پیش رو دارد: نگه داشتن باغ با هزینه فرصت سنگین، یا از بین بردن
// تدریجی درختان تا پلاک «بایر» شود و بشود برایش پروانه گرفت. تجربه نشان
// می‌دهد راه دوم زیاد انتخاب می‌شود.
//
// هم‌زمان، در محورهای توسعه‌ای شهر تقاضای تراکم مازاد وجود دارد که امروز
// یا از مسیر کمیسیون ماده ۵ می‌گذرد (کند و موردی) یا اصلاً محقق نمی‌شود.
//
// ── سازوکار ──────────────────────────────────────────────────────────────
// مالک باغ گواهی حق توسعه می‌گیرد، معادلش توکن دریافت می‌کند، و آن را در
// این بازار می‌فروشد. خریدار در پهنه گیرنده با همان توکن مازاد تراکمش را
// پوشش می‌دهد. شهرداری از هر معامله کارمزد می‌گیرد.
//
// نتیجه: باغ سرجایش می‌ماند چون حفظش سودآور شده، تراکم به جایی رفته که
// زیرساخت دارد، و شهرداری درآمد پایداری پیدا کرده که برخلاف تراکم‌فروشی
// مستقیم، ظرفیت کل شهر را بالا نمی‌برد — فقط جابه‌جا می‌کند.
//
// ⚠️ این قرارداد عمداً روی همان کانال SqmToken است. معامله باید اتمی باشد:
// انتقال توکن و ثبت درآمد و بستن عرضه یا همه با هم اتفاق می‌افتند یا هیچ‌کدام.
// روی کانال جدا، InvokeChaincode فقط خواندنی می‌شد و این تضمین از بین می‌رفت.
// ---------------------------------------------------------------------------

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/hyperledger/fabric-contract-api-go/contractapi"
)

type TdrMarketContract struct {
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

const (
	OfferOpen      = "OPEN"
	OfferPartial   = "PARTIAL"
	OfferFilled    = "FILLED"
	OfferCancelled = "CANCELLED"
)

// Offer — عرضه توکن تراکم در بازار.
type Offer struct {
	DocType string `json:"docType"`
	ID      string `json:"id"`
	Seller  string `json:"seller"`
	// SourceParcel — پلاک فرستنده. برای شفافیت نگه داشته می‌شود: خریدار باید
	// بتواند ببیند تراکمی که می‌خرد از کدام باغ یا کدام پلاک آمده است.
	SourceParcel string `json:"sourceParcel"`
	Use          string `json:"use"`
	Region       string `json:"region"`
	Amount       int64  `json:"amount"`    // متر مربع عرضه‌شده
	Remaining    int64  `json:"remaining"`
	PricePerSqm  int64  `json:"pricePerSqm"` // ریال بر متر مربع
	// MunicipalOffer — عرضه از سوی خود شهرداری (حراج ظرفیت).
	MunicipalOffer bool   `json:"municipalOffer"`
	Status         string `json:"status"`
	CreatedAt      int64  `json:"createdAt"`
	UpdatedAt      int64  `json:"updatedAt"`
}

// Trade — یک معامله انجام‌شده.
type Trade struct {
	DocType     string `json:"docType"`
	ID          string `json:"id"`
	OfferID     string `json:"offerId"`
	Seller      string `json:"seller"`
	Buyer       string `json:"buyer"`
	Use         string `json:"use"`
	Amount      int64  `json:"amount"`
	PricePerSqm int64  `json:"pricePerSqm"`
	GrossValue  int64  `json:"grossValue"`
	MunicipalFee int64 `json:"municipalFee"`
	NetToSeller int64  `json:"netToSeller"`
	At          int64  `json:"at"`
}

// ---------------------------- عرضه ----------------------------

// CreateOffer ثبت عرضه. توکن‌ها همان لحظه از موجودی فروشنده به حساب امانی
// بازار منتقل می‌شوند — وگرنه فروشنده می‌تواند همان توکن را هم‌زمان در چند
// عرضه بگذارد یا خرجش کند و معامله در لحظه تسویه شکست بخورد.
func (c *TdrMarketContract) CreateOffer(ctx contractapi.TransactionContextInterface,
	offerID, seller, sourceParcel, use, region string, amount, pricePerSqm int64) (*Offer, error) {

	if err := requireRole(ctx, RoleCitizen, RoleRegulator, RoleDistrict, RoleFinance); err != nil {
		return nil, err
	}
	if amount <= 0 || pricePerSqm <= 0 {
		return nil, fmt.Errorf("متراژ و قیمت باید مثبت باشند")
	}
	dup, err := ctx.GetStub().GetState(mkKey(KeyOffer, offerID))
	if err != nil {
		return nil, err
	}
	if dup != nil {
		return nil, fmt.Errorf("عرضه با شناسه «%s» قبلاً ثبت شده است", offerID)
	}

	municipal := seller == "MUNICIPALITY"
	if municipal {
		// حراج ظرفیت توسط شهرداری فقط با نقش تنظیم‌گر.
		if err := requireRole(ctx, RoleRegulator, RoleFinance); err != nil {
			return nil, err
		}
	}

	// انتقال به حساب امانی بازار.
	if _, err := call(ctx, "SqmToken", "Transfer",
		use, seller, escrowAccount(offerID), fmt.Sprintf("%d", amount), "0",
		"واریز به امانی بازار تراکم"); err != nil {
		return nil, err
	}

	now := txTime(ctx)
	o := Offer{
		DocType: "offer", ID: offerID, Seller: seller, SourceParcel: sourceParcel,
		Use: use, Region: region, Amount: amount, Remaining: amount,
		PricePerSqm: pricePerSqm, MunicipalOffer: municipal,
		Status: OfferOpen, CreatedAt: now, UpdatedAt: now,
	}
	if err := putJSON(ctx, mkKey(KeyOffer, offerID), &o); err != nil {
		return nil, err
	}
	if err := emit(ctx, "OfferCreated", offerID,
		fmt.Sprintf("عرضه %d م² توکن %s در منطقه %s به قیمت %d ریال/م² (فروشنده %s، مبدأ %s)",
			amount, "SQM-"+use, region, pricePerSqm, seller, sourceParcel), amount); err != nil {
		return nil, err
	}
	return &o, nil
}

func escrowAccount(offerID string) string { return "MARKET_ESCROW:" + offerID }

// CancelOffer لغو عرضه و بازگشت توکن‌های امانی به فروشنده.
func (c *TdrMarketContract) CancelOffer(ctx contractapi.TransactionContextInterface,
	offerID string) (*Offer, error) {

	if err := requireRole(ctx, RoleCitizen, RoleRegulator, RoleDistrict, RoleFinance); err != nil {
		return nil, err
	}
	var o Offer
	if err := mustGetJSON(ctx, mkKey(KeyOffer, offerID), &o, "عرضه"); err != nil {
		return nil, err
	}
	if o.Status == OfferFilled || o.Status == OfferCancelled {
		return nil, fmt.Errorf("عرضه «%s» در وضعیت «%s» است و قابل لغو نیست", offerID, o.Status)
	}
	if o.Remaining > 0 {
		if _, err := call(ctx, "SqmToken", "Transfer",
			o.Use, escrowAccount(offerID), o.Seller, fmt.Sprintf("%d", o.Remaining), "0",
			"بازگشت امانی پس از لغو عرضه"); err != nil {
			return nil, err
		}
	}
	o.Status = OfferCancelled
	o.UpdatedAt = txTime(ctx)
	if err := putJSON(ctx, mkKey(KeyOffer, offerID), &o); err != nil {
		return nil, err
	}
	if err := emit(ctx, "OfferCancelled", offerID,
		fmt.Sprintf("عرضه %s لغو شد؛ %d م² به فروشنده بازگشت", offerID, o.Remaining), o.Remaining); err != nil {
		return nil, err
	}
	return &o, nil
}

// ---------------------------- معامله ----------------------------

// ExecuteTrade انجام معامله. سازمان مالی پس از تأیید پرداخت خریدار آن را
// ثبت می‌کند؛ سه اثر (انتقال توکن، ثبت کارمزد، به‌روزرسانی عرضه) در یک
// تراکنش اتمی‌اند.
func (c *TdrMarketContract) ExecuteTrade(ctx contractapi.TransactionContextInterface,
	tradeID, offerID, buyer string, amount int64) (*Trade, error) {

	if err := requireRole(ctx, RoleFinance, RoleRegulator); err != nil {
		return nil, err
	}
	dup, err := ctx.GetStub().GetState(mkKey("TRADE", tradeID))
	if err != nil {
		return nil, err
	}
	if dup != nil {
		return nil, fmt.Errorf("معامله با شناسه «%s» قبلاً ثبت شده است", tradeID)
	}
	var o Offer
	if err := mustGetJSON(ctx, mkKey(KeyOffer, offerID), &o, "عرضه"); err != nil {
		return nil, err
	}
	if o.Status != OfferOpen && o.Status != OfferPartial {
		return nil, fmt.Errorf("عرضه «%s» در وضعیت «%s» است و معامله نمی‌پذیرد", offerID, o.Status)
	}
	if amount <= 0 || amount > o.Remaining {
		return nil, fmt.Errorf("متراژ درخواستی نامعتبر است (باقی‌مانده عرضه: %d م²)", o.Remaining)
	}
	if buyer == o.Seller {
		return nil, fmt.Errorf("خریدار و فروشنده یکسان‌اند؛ معامله صوری ثبت نمی‌شود")
	}

	// کارمزد شهرداری از دفترچه تعرفه سال جاری.
	feePM := int64(50) // ۵٪ پیش‌فرض
	year := 1970 + txTime(ctx)/SecondsPerYear
	if tb, e := call(ctx, "Regulation", "GetTariff", fmt.Sprintf("%d", year)); e == nil {
		var t Tariff
		if json.Unmarshal(tb, &t) == nil && t.TdrFeePerMille > 0 {
			feePM = t.TdrFeePerMille
		}
	}
	gross := amount * o.PricePerSqm
	fee := perMille(gross, feePM)

	// انتقال توکن از امانی به خریدار. کارمزد از توکن گرفته نمی‌شود، از پول
	// گرفته می‌شود — خریدار باید دقیقاً همان متراژی را بگیرد که خریده، وگرنه
	// محاسبه پوشش مازادش غلط از آب درمی‌آید.
	if _, err := call(ctx, "SqmToken", "Transfer",
		o.Use, escrowAccount(offerID), buyer, fmt.Sprintf("%d", amount), "0",
		"تسویه معامله بازار تراکم"); err != nil {
		return nil, err
	}
	if _, err := call(ctx, "Treasury", "BookExternalRevenue",
		"TDR_FEE", fmt.Sprintf("%d", fee),
		fmt.Sprintf("کارمزد معامله %s — %d م² × %d ریال", tradeID, amount, o.PricePerSqm)); err != nil {
		return nil, err
	}
	// عرضه شهرداری: کل عایدی درآمد شهرداری است، نه فقط کارمزد.
	if o.MunicipalOffer {
		if _, err := call(ctx, "Treasury", "BookExternalRevenue",
			"TDR_SALE", fmt.Sprintf("%d", gross-fee),
			fmt.Sprintf("فروش ظرفیت تراکم — عرضه %s", offerID)); err != nil {
			return nil, err
		}
	}

	now := txTime(ctx)
	o.Remaining -= amount
	if o.Remaining == 0 {
		o.Status = OfferFilled
	} else {
		o.Status = OfferPartial
	}
	o.UpdatedAt = now
	if err := putJSON(ctx, mkKey(KeyOffer, offerID), &o); err != nil {
		return nil, err
	}

	t := Trade{
		DocType: "trade", ID: tradeID, OfferID: offerID, Seller: o.Seller,
		Buyer: buyer, Use: o.Use, Amount: amount, PricePerSqm: o.PricePerSqm,
		GrossValue: gross, MunicipalFee: fee, NetToSeller: gross - fee, At: now,
	}
	if err := putJSON(ctx, mkKey("TRADE", tradeID), &t); err != nil {
		return nil, err
	}
	if err := emit(ctx, "TradeExecuted", tradeID,
		fmt.Sprintf("معامله %d م² توکن %s از «%s» به «%s» — ارزش %d ریال، کارمزد شهرداری %d ریال",
			amount, "SQM-"+o.Use, o.Seller, buyer, gross, fee), gross); err != nil {
		return nil, err
	}
	return &t, nil
}

// ---------------------------- پرس‌وجو و کشف قیمت ----------------------------

func (c *TdrMarketContract) GetOffer(ctx contractapi.TransactionContextInterface,
	offerID string) (*Offer, error) {
	var o Offer
	if err := mustGetJSON(ctx, mkKey(KeyOffer, offerID), &o, "عرضه"); err != nil {
		return nil, err
	}
	return &o, nil
}

// OrderBook دفتر سفارش‌های باز. فیلتر منطقه و کاربری اختیاری است.
func (c *TdrMarketContract) OrderBook(ctx contractapi.TransactionContextInterface,
	region, use string) ([]Offer, error) {
	raw, err := queryByPrefix(ctx, KeyOffer)
	if err != nil {
		return nil, err
	}
	out := []Offer{}
	for _, b := range raw {
		var o Offer
		if err := json.Unmarshal(b, &o); err != nil {
			continue
		}
		if o.Status != OfferOpen && o.Status != OfferPartial {
			continue
		}
		if region != "" && o.Region != region {
			continue
		}
		if use != "" && o.Use != use {
			continue
		}
		out = append(out, o)
	}
	return out, nil
}

// MarketStat — آمار بازار یک کاربری در یک منطقه.
type MarketStat struct {
	Region        string `json:"region"`
	Use           string `json:"use"`
	TitleFa       string `json:"titleFa"`
	OpenOffers    int64  `json:"openOffers"`
	OpenVolume    int64  `json:"openVolume"`
	MinPrice      int64  `json:"minPrice"`
	MaxPrice      int64  `json:"maxPrice"`
	TradedVolume  int64  `json:"tradedVolume"`
	TradedValue   int64  `json:"tradedValue"`
	AvgTradePrice int64  `json:"avgTradePrice"`
	MunicipalFees int64  `json:"municipalFees"`
}

// MarketReport آمار بازار.
//
// AvgTradePrice مهم‌ترین عدد این گزارش است: قیمت واقعی هر متر تراکم در هر
// منطقه، کشف‌شده در بازار. شهرداری امروز این عدد را ندارد و عوارض تراکم
// مازاد را با ضرایبی می‌گیرد که رابطه‌اش با ارزش واقعی معلوم نیست. با این
// عدد، تعرفه سال بعد را می‌شود بر پایه داده تنظیم کرد نه بر پایه چانه‌زنی.
func (c *TdrMarketContract) MarketReport(ctx contractapi.TransactionContextInterface,
	region string) ([]MarketStat, error) {

	offers, err := queryByPrefix(ctx, KeyOffer)
	if err != nil {
		return nil, err
	}
	trades, err := queryByPrefix(ctx, "TRADE")
	if err != nil {
		return nil, err
	}

	agg := map[string]*MarketStat{}
	key := func(r, u string) string { return r + "|" + u }

	for _, b := range offers {
		var o Offer
		if err := json.Unmarshal(b, &o); err != nil {
			continue
		}
		if region != "" && o.Region != region {
			continue
		}
		k := key(o.Region, o.Use)
		s, ok := agg[k]
		if !ok {
			s = &MarketStat{Region: o.Region, Use: o.Use, TitleFa: UseTitleFa[o.Use]}
			agg[k] = s
		}
		if o.Status == OfferOpen || o.Status == OfferPartial {
			s.OpenOffers++
			s.OpenVolume += o.Remaining
			if s.MinPrice == 0 || o.PricePerSqm < s.MinPrice {
				s.MinPrice = o.PricePerSqm
			}
			if o.PricePerSqm > s.MaxPrice {
				s.MaxPrice = o.PricePerSqm
			}
		}
	}

	// معامله‌ها منطقه ندارند؛ از عرضه مربوطه گرفته می‌شود.
	offerRegion := map[string]string{}
	for _, b := range offers {
		var o Offer
		if json.Unmarshal(b, &o) == nil {
			offerRegion[o.ID] = o.Region
		}
	}
	for _, b := range trades {
		var t Trade
		if err := json.Unmarshal(b, &t); err != nil {
			continue
		}
		r := offerRegion[t.OfferID]
		if region != "" && r != region {
			continue
		}
		k := key(r, t.Use)
		s, ok := agg[k]
		if !ok {
			s = &MarketStat{Region: r, Use: t.Use, TitleFa: UseTitleFa[t.Use]}
			agg[k] = s
		}
		s.TradedVolume += t.Amount
		s.TradedValue += t.GrossValue
		s.MunicipalFees += t.MunicipalFee
	}

	// خروجی به ترتیب ثابت: منطقه ۱ تا ۱۱، سپس کاربری‌ها به ترتیب AllUses.
	out := []MarketStat{}
	for i := 1; i <= 11; i++ {
		r := fmt.Sprintf("%d", i)
		for _, u := range AllUses {
			if s, ok := agg[key(r, u)]; ok {
				if s.TradedVolume > 0 {
					s.AvgTradePrice = s.TradedValue / s.TradedVolume
				}
				out = append(out, *s)
			}
		}
	}
	return out, nil
}

func main() {
	cc, err := contractapi.NewChaincode(&TdrMarketContract{})
	if err != nil {
		log.Panicf("خطا در ساخت قرارداد TdrMarket: %v", err)
	}
	if err := cc.Start(); err != nil {
		log.Panicf("خطا در اجرای قرارداد TdrMarket: %v", err)
	}
}
