package kfx

import (
	"strconv"
	"strings"

	"github.com/amzn/ion-go/ion"
	"go.uber.org/zap"
)

var ionBVM = []byte{0xE0, 1, 0, 0xEA} // binary version marker

func createSymbolToken(symbol string, stb ion.SymbolTableBuilder) ion.SymbolToken {

	if !strings.HasPrefix(symbol, "$") {
		if stb != nil {
			sid, _ := stb.Add(symbol)
			return ion.SymbolToken{Text: &symbol, LocalSID: int64(sid)}
		}
	} else {
		if sid, err := strconv.ParseInt(symbol[1:], 10, 64); err == nil {
			// Strictly speaking this is only good while sid < YJ_symbols.MaxID
			return ion.SymbolToken{Text: &symbol, LocalSID: sid}
		}
	}
	return ion.SymbolToken{Text: &symbol, LocalSID: ion.SymbolIDUnknown}
}

func createLocalSymbolToken(symbol string, log *zap.Logger) ion.SymbolToken {

	if strings.HasPrefix(symbol, "$") {
		if sid, err := strconv.ParseInt(symbol[1:], 10, 64); err == nil {
			// Strictly speaking this is only good while sid < YJ_symbols.MaxID
			return ion.SymbolToken{Text: &symbol, LocalSID: sid}
		}
	}
	// cannot parse symbol name - should never happen
	log.Warn("Unable to interpret local ion symbol", zap.String("symbol", symbol))
	return ion.SymbolToken{Text: &symbol, LocalSID: ion.SymbolIDUnknown}
}
