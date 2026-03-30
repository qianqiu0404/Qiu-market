package service

import (
	"github.com/the-web3/s78-market-services/database"
	"github.com/the-web3/s78-market-services/services/http/model"
)

type RestService interface {
	GetSupportAsset(*model.SupportAssetRequest) (*model.SupportAssetResponse, error)
}

type HandleSvc struct {
	assetView database.AssetView
}

func NewHandleSvc(assetView database.AssetView) RestService {
	return &HandleSvc{
		assetView: assetView,
	}
}
