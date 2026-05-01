package marketkey

func Build(exchangeGuid, exchangeName, symbolGuid, symbolName string) string {
	return exchangeGuid + "%" + exchangeName + "%" + symbolGuid + "%" + symbolName
}
