package database

import (
	"time"

	"github.com/ethereum/go-ethereum/log"
	"gorm.io/gorm"
)

type Asset struct {
	Guid        string    `gorm:"primaryKey;column:guid;type:text" json:"guid"`
	AssetName   string    `gorm:"column:asset_name;type:varchar(50);not null;default:'Tether USDT'" json:"asset_name"`
	AssetSymbol string    `gorm:"column:asset_symbol;type:varchar(20);not null;default:'USDT'" json:"asset_symbol"`
	AssetLogo   string    `gorm:"column:asset_logo;type:varchar(50);not null;default:''" json:"asset_logo"`
	IsActive    bool      `gorm:"column:is_active;type:boolean;not null;default:true" json:"is_active"`
	CreatedAt   time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (Asset) TableName() string {
	return "asset"
}

type AssetView interface {
	QueryAssets() ([]*Asset, error)
	QueryAssetList(page, pageSize int64) ([]*Asset, int64, error)
}

type AssetDB interface {
	AssetView

	StoreAssets([]Asset) error
	StoreAsset(*Asset) error
}

type assetDB struct {
	gorm *gorm.DB
}

func NewAssetDB(db *gorm.DB) AssetDB {
	return &assetDB{gorm: db}
}

func (a *assetDB) QueryAssetList(page, pageSize int64) ([]*Asset, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 10
	}

	offset := (page - 1) * pageSize

	var list []*Asset
	query := a.gorm.Model(&Asset{})

	var total int64
	if err := query.Count(&total).Error; err != nil {
		log.Error("Failed to query asset list count", "error", err)
		return nil, 0, err
	}
	if err := query.Order("created_at DESC").Limit(int(pageSize)).Offset(int(offset)).Find(&list).Error; err != nil {
		log.Error("Failed to query asset list by page and pageSize", "error", err)
		return nil, 0, err
	}
	return list, total, nil
}

func (a *assetDB) QueryAssets() ([]*Asset, error) {
	var list []*Asset
	if err := a.gorm.Table("asset").Find(&list).Error; err != nil {
		log.Error("Failed to query assets", "error", err)
		return nil, err
	}
	return list, nil
}

func (a *assetDB) StoreAssets(assets []Asset) error {
	if err := a.gorm.Table("asset").CreateInBatches(&assets, len(assets)).Error; err != nil {
		log.Error("Failed to store asset list", "error", err)
		return err
	}
	return nil
}

func (a *assetDB) StoreAsset(asset *Asset) error {
	if err := a.gorm.Table("asset").Create(&asset).Error; err != nil {
		log.Error("Failed to store asset", "error", err)
		return err
	}
	return nil
}
