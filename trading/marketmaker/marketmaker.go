package marketmaker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/the-web3/s78-market-services/trading/domain"
)

var ErrUnsafeReference = errors.New("reference price is unsafe")

type Reference struct {
	Price      int64
	ObservedAt time.Time
}

type ReferenceSource interface {
	Current(context.Context) (Reference, error)
}

type Engine interface {
	Submit(context.Context, domain.NewOrder) (domain.Result, error)
	Cancel(context.Context, domain.CancelOrder) (domain.Result, error)
	Orders(domain.AccountID, bool) ([]domain.Order, error)
}

type Config struct {
	AccountID     domain.AccountID
	SpreadsBPS    []int64
	Quantity      int64
	MaxAge        time.Duration
	MaxJumpBPS    int64
	RefreshEvery  time.Duration
	RequestPrefix string
}

func DefaultConfig() Config {
	return Config{
		AccountID:     "system:demo-maker",
		SpreadsBPS:    []int64{10, 25, 50},
		Quantity:      1_000_000,
		MaxAge:        30 * time.Second,
		MaxJumpBPS:    500,
		RefreshEvery:  5 * time.Second,
		RequestPrefix: "demo-maker",
	}
}

type Maker struct {
	market        domain.Market
	engine        Engine
	source        ReferenceSource
	config        Config
	now           func() time.Time
	counter       uint64
	previousPrice int64
}

func New(
	market domain.Market,
	engine Engine,
	source ReferenceSource,
	config Config,
) (*Maker, error) {
	if err := market.Validate(); err != nil {
		return nil, err
	}
	if engine == nil || source == nil || config.AccountID == "" ||
		config.Quantity <= 0 || config.MaxAge <= 0 || config.RefreshEvery <= 0 ||
		config.MaxJumpBPS <= 0 || config.MaxJumpBPS >= 10_000 ||
		config.RequestPrefix == "" || len(config.SpreadsBPS) == 0 {
		return nil, fmt.Errorf("invalid demo maker configuration")
	}
	if config.Quantity%market.QuantityStep != 0 || config.Quantity < market.MinQuantity {
		return nil, fmt.Errorf("demo maker quantity does not satisfy market rules")
	}
	for _, spread := range config.SpreadsBPS {
		if spread <= 0 || spread >= 10_000 {
			return nil, fmt.Errorf("demo maker spread must be in (0,10000)")
		}
	}
	return &Maker{
		market: market,
		engine: engine,
		source: source,
		config: config,
		now:    time.Now,
	}, nil
}

func (m *Maker) Run(ctx context.Context) error {
	if err := m.refresh(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(m.config.RefreshEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			cancelContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			cancelErr := m.cancelAll(cancelContext)
			cancel()
			if cancelErr != nil {
				return cancelErr
			}
			return nil
		case <-ticker.C:
			if err := m.refresh(ctx); err != nil {
				return err
			}
		}
	}
}

func (m *Maker) Refresh(ctx context.Context) error {
	return m.refresh(ctx)
}

func (m *Maker) refresh(ctx context.Context) error {
	reference, err := m.source.Current(ctx)
	if err != nil {
		return m.stopUnsafe(ctx, fmt.Errorf("%w: source error: %v", ErrUnsafeReference, err))
	}
	if err := m.validateReference(reference); err != nil {
		return m.stopUnsafe(ctx, err)
	}
	if err := m.cancelAll(ctx); err != nil {
		return fmt.Errorf("cancel previous demo-maker quotes: %w", err)
	}

	for _, spread := range m.config.SpreadsBPS {
		bid, ask, err := m.quotePrices(reference.Price, spread)
		if err != nil {
			return m.stopUnsafe(ctx, err)
		}
		for _, quote := range []struct {
			side  domain.Side
			price int64
			label string
		}{
			{side: domain.SideBuy, price: bid, label: "bid"},
			{side: domain.SideSell, price: ask, label: "ask"},
		} {
			m.counter++
			requestID := fmt.Sprintf(
				"%s-%s-%04dbps-%020d",
				m.config.RequestPrefix,
				quote.label,
				spread,
				m.counter,
			)
			result, submitErr := m.engine.Submit(ctx, domain.NewOrder{
				ClientOrderID: requestID,
				AccountID:     m.config.AccountID,
				Side:          quote.side,
				Type:          domain.OrderTypeLimit,
				TimeInForce:   domain.TimeInForceGTC,
				PostOnly:      true,
				Price:         quote.price,
				Quantity:      m.config.Quantity,
			})
			if submitErr != nil || result.Status != domain.OrderStatusOpen {
				cancelContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_ = m.cancelAll(cancelContext)
				cancel()
				if submitErr != nil {
					return fmt.Errorf("submit demo-maker %s: %w", quote.label, submitErr)
				}
				return fmt.Errorf("submit demo-maker %s rejected with status %s",
					quote.label, result.Status)
			}
		}
	}
	m.previousPrice = reference.Price
	return nil
}

func (m *Maker) validateReference(reference Reference) error {
	if reference.Price <= 0 || reference.ObservedAt.IsZero() ||
		m.now().UTC().Sub(reference.ObservedAt.UTC()) > m.config.MaxAge ||
		reference.ObservedAt.After(m.now().UTC().Add(time.Second)) {
		return fmt.Errorf("%w: stale or invalid observation", ErrUnsafeReference)
	}
	if m.previousPrice > 0 {
		difference := reference.Price - m.previousPrice
		if difference < 0 {
			difference = -difference
		}
		jumpBPS, err := domain.CheckedMulDivCeil(difference, 10_000, m.previousPrice)
		if err != nil {
			return fmt.Errorf("%w: jump calculation: %v", ErrUnsafeReference, err)
		}
		if jumpBPS > m.config.MaxJumpBPS {
			return fmt.Errorf("%w: reference jump is %d bps", ErrUnsafeReference, jumpBPS)
		}
	}
	return nil
}

func (m *Maker) quotePrices(referencePrice, spreadBPS int64) (int64, int64, error) {
	bid, err := domain.CheckedMulDivFloor(referencePrice, 10_000-spreadBPS, 10_000)
	if err != nil {
		return 0, 0, err
	}
	ask, err := domain.CheckedMulDivCeil(referencePrice, 10_000+spreadBPS, 10_000)
	if err != nil {
		return 0, 0, err
	}
	bid -= bid % m.market.PriceTick
	ask, err = ceilToStep(ask, m.market.PriceTick)
	if err != nil {
		return 0, 0, err
	}
	if bid <= 0 || ask <= bid {
		return 0, 0, fmt.Errorf("%w: derived quote prices are invalid", ErrUnsafeReference)
	}
	return bid, ask, nil
}

func (m *Maker) stopUnsafe(ctx context.Context, cause error) error {
	cancelErr := m.cancelAll(ctx)
	if cancelErr != nil {
		return errors.Join(cause, fmt.Errorf("cancel unsafe demo-maker quotes: %w", cancelErr))
	}
	return cause
}

func (m *Maker) cancelAll(ctx context.Context) error {
	orders, err := m.engine.Orders(m.config.AccountID, true)
	if err != nil {
		return err
	}
	orderIDs := make([]domain.OrderID, 0, len(orders))
	for _, order := range orders {
		orderIDs = append(orderIDs, order.ID)
	}
	return m.cancelOrderIDs(ctx, orderIDs)
}

func (m *Maker) cancelOrderIDs(ctx context.Context, orderIDs []domain.OrderID) error {
	var result error
	for _, orderID := range orderIDs {
		m.counter++
		_, err := m.engine.Cancel(ctx, domain.CancelOrder{
			RequestID: fmt.Sprintf("%s-cancel-%020d", m.config.RequestPrefix, m.counter),
			AccountID: m.config.AccountID,
			OrderID:   orderID,
		})
		if err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

func ceilToStep(value, step int64) (int64, error) {
	remainder := value % step
	if remainder == 0 {
		return value, nil
	}
	return domain.CheckedAdd(value, step-remainder)
}
