package i18n

// value_language_test.go (T180, ct-2026-11-10-2342) — el guardarraíl que
// faltaba: los tests de este paquete cruzaban CLAVES, nunca el IDIOMA del
// valor.
//
// Lo que ya existía y por qué no alcanzaba:
//   - TestCatalogsAreComplete / TestAllKeysMatchBetweenFrontendAndCatalog
//     (catalog_usage_test.go): que cada clave exista en los dos mapas y que
//     alguien la use. Nada sobre lo que DICE el valor.
//   - TestNoUntranslatedProseInKnownSinks (prose_sink_test.go): que app.js
//     no escriba prosa suelta sin pasar por t(). Mira app.js, no el catálogo.
//   - TestIndexHTMLElementsHaveI18n: que el marcado tenga data-i18n.
// Las tres pasan en verde con el catálogo español lleno de inglés.
//
// El caso que lo motivó (T172d, 2026-11-10): clients.chart_title y
// clients.chart_limit quedaron con el texto INGLÉS dentro de esCatalog. Una
// edición por script buscó la clave y reemplazó la primera coincidencia, dos
// veces por clave — así que la substitución inglesa pisó la española. Se
// descubrió recién al servir el catálogo real y mirar la pantalla.
//
// MEDIDO antes de escribir esto (ct-2026-11-10-2342, medidores temporales):
// 14 valores de 318 son idénticos en es y en hoy, y los 14 son legítimos —
// están abajo con su motivo. Ese 14 es el control: si el número sube sin que
// alguien agregue una excepción justificada, es una clave sin traducir.

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// identicalValueExceptions: los valores que SÍ son iguales en los dos
// idiomas a propósito, con el porqué al lado. Misma forma (y mismo
// espíritu) que indexHTMLI18nExceptions y allowedNonProseLiterals: una
// excepción acá es una decisión que se justifica por escrito, no un efecto
// lateral de la regla.
//
// Antes de agregar una entrada, la pregunta es "¿un lector inglés leería
// esto igual que un lector español?". Si la respuesta es no, no es una
// excepción: es una traducción que falta.
var identicalValueExceptions = map[string]string{
	// Siglas e identificadores técnicos — no se traducen en ningún idioma.
	"agent.label_pin":         "sigla (PIN), igual en los dos idiomas — ya documentado en el bloque de agent.label_endpoint de catalog.go",
	"agent.label_terminal_id": "identificador técnico de la antena, se escribe igual en los dos idiomas",
	"agent.label_endpoint":    "préstamo inglés ya establecido para este label, decisión de la etapa 2b-iii (ver catalog.go)",
	"agent.id_prefix":         "la sigla ID más puntuación, sin palabra traducible",
	"clients.cost_label":      "símbolo de moneda (US$), no es texto",

	// Préstamos ingleses YA decididos para este tablero, documentados en
	// catalog.go cuando se tomó la decisión. Cambiarlos es una decisión de
	// producto, no un arreglo de traducción.
	"tab.chats":             "préstamo inglés establecido en este tablero técnico (T165) — la decisión está escrita en catalog.go",
	"tab.drafts":            "mismo caso que tab.chats, misma decisión de T165",
	"media.sticker_alt":     "\"sticker\" es el término corriente en español rioplatense para esto; traducirlo sonaría ajeno",
	"badge.governor_killed": "\"kill\" y \"Governor\" son préstamos ya establecidos acá (ver el comentario de badge.governor_killed en catalog.go)",

	// Palabras que se escriben igual en los dos idiomas.
	"rules.general_label": "\"General\" se escribe igual en español y en inglés",
	"error.prefix":        "\"Error\" se escribe igual en los dos idiomas; el resto es puntuación",
	"chat.alias_prefix":   "\"alias\" es la misma palabra en los dos idiomas; el resto es puntuación",

	// Plantillas sin prosa: variables, emoji y abreviaturas que no cambian.
	"sync.summary":      "sólo variables, emoji y abreviaturas (chats/msgs) que no se traducen",
	"agent.ping_button": "\"Ping\" es préstamo establecido, más un emoji — sin prosa que traducir",
}

// TestNoUntranslatedIdenticalValues: falla si un valor es idéntico en es y
// en sin estar declarado arriba. Es la red para el error más común al sumar
// una clave: copiarla al segundo mapa y olvidar traducirla, que los tests de
// claves ven perfectamente sano.
func TestNoUntranslatedIdenticalValues(t *testing.T) {
	for key, es := range esCatalog {
		en, ok := enCatalog[key]
		if !ok {
			continue // ésa es la falla de TestCatalogsAreComplete, no de ésta
		}
		if es != en {
			continue
		}
		if _, allowed := identicalValueExceptions[key]; allowed {
			continue
		}
		t.Errorf("%s tiene el MISMO valor en es y en en (%q) — si es una traducción que falta, "+
			"tradúcela; si de verdad es igual en los dos idiomas, agrégala a "+
			"identicalValueExceptions con el motivo", key, es)
	}
}

// TestIdenticalValueExceptionsAreAlive: la otra dirección, la que mantiene
// honesta la lista. Una excepción que ya no corresponde (porque la clave se
// tradujo, se renombró o se borró) deja de ser una decisión documentada y
// pasa a ser un permiso olvidado que taparía un defecto futuro con ese mismo
// nombre. Mismo criterio con que catalog_usage_test.go persigue las claves
// que ya nadie usa.
func TestIdenticalValueExceptionsAreAlive(t *testing.T) {
	for key, reason := range identicalValueExceptions {
		es, inES := esCatalog[key]
		en, inEN := enCatalog[key]
		if !inES || !inEN {
			t.Errorf("identicalValueExceptions tiene %q (%s) pero esa clave ya no está en los dos "+
				"catálogos — borra la excepción", key, reason)
			continue
		}
		if es != en {
			t.Errorf("identicalValueExceptions tiene %q (%s) pero sus valores YA son distintos "+
				"(es=%q, en=%q) — la excepción sobra, borrala", key, reason, es, en)
		}
	}
}

// ── C: inglés dentro del bloque español ────────────────────────────────
// La parte que atrapa el caso REAL de T172d. El test de arriba no lo ve: en
// aquel defecto el valor español quedó en inglés, pero el inglés tenía OTRO
// texto (el viejo), así que los dos nunca fueron iguales. Medido sobre el
// archivo tal como quedó:
//   clients.chart_title  es="lines per week (last 12)"   en="12 weeks"
//   clients.chart_limit  es="weekly limit: {lines} lines" en="weekly limit"
//
// La señal son las palabras FUNCIÓN inglesas, nunca los sustantivos. Este
// tablero está lleno de préstamos técnicos ya decididos (backup, endpoint,
// dashboard, drift, chat, sticker, governor, kill, sync, ping): una lista de
// "palabras inglesas" los marcaría a todos y el guardarraíl moriría de
// excepciones. Las palabras función no se toman prestadas: si "per", "week"
// o "your" aparecen en un valor de es, o es texto inglés o es una cita.
//
// MEDIDO antes de escribirlo: sobre el catálogo de hoy esta lista marca UNA
// sola clave (modal.about_heading, abajo), y marca los DOS valores rotos de
// T172d. Los mismos pares ya corregidos dan cero.

// englishFunctionWords: palabras que el español de este tablero no toma
// prestadas NUNCA.
//
// REGLA DE MANTENIMIENTO (condición del contrato): si alguna de éstas se
// volviera un préstamo legítimo en algún texto, la que se agrega a
// englishInSpanishExceptions es LA CLAVE, con su motivo — la palabra NO se
// saca de esta lista. Sacarla apagaría la señal para todas las demás claves
// de golpe y en silencio; una excepción por clave deja el permiso a la vista
// y acotado a su caso.
//
// Deliberadamente FUERA: toda palabra que también exista en español ("no",
// "has", "son", "van", "este", "para", "sin", "ante") y todo sustantivo
// técnico que el tablero ya usa como préstamo.
var englishFunctionWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "without": true,
	"from": true, "your": true, "yours": true, "this": true, "that": true,
	"these": true, "those": true, "there": true, "their": true, "they": true,
	"them": true, "than": true, "then": true, "what": true, "when": true,
	"where": true, "which": true, "who": true, "why": true, "how": true,
	"week": true, "weeks": true, "weekly": true, "daily": true, "monthly": true,
	"last": true, "next": true, "only": true, "each": true, "every": true,
	"into": true, "onto": true, "upon": true, "over": true, "under": true,
	"above": true, "below": true, "after": true, "before": true, "during": true,
	"while": true, "until": true, "since": true, "between": true, "among": true,
	"through": true, "within": true, "against": true, "toward": true,
	"been": true, "being": true, "were": true, "was": true, "are": true,
	"will": true, "would": true, "should": true, "could": true, "must": true,
	"other": true, "another": true, "such": true, "same": true, "both": true,
	"either": true, "neither": true, "here": true, "soon": true, "again": true,
	"once": true, "twice": true, "always": true, "never": true, "often": true,
	"also": true, "just": true, "still": true, "already": true, "yet": true,
	"more": true, "most": true, "many": true, "much": true, "very": true,
	"please": true, "thanks": true, "thank": true, "sorry": true,
	"per": true, "about": true, "its": true,
}

// englishInSpanishExceptions: las claves de es donde una de esas palabras
// aparece a propósito, con el porqué. Medido: hoy es una sola.
var englishInSpanishExceptions = map[string]string{
	"modal.about_heading": "cita el término técnico \"About\" entre comillas — es el nombre de la pantalla que se está nombrando, no prosa inglesa",
}

var (
	i18nVarHole     = regexp.MustCompile(`\{[^}]*\}`)
	i18nWordPattern = regexp.MustCompile(`[\p{L}']+`)
)

// englishHitsIn: las palabras función inglesas de un valor español. Los
// huecos {name} se descartan ANTES de mirar: sus nombres son en inglés por
// convención en todo el catálogo ({when}, {chats}, {code}) y son idénticos
// en los dos idiomas por contrato. Sin descartarlos, cuatro claves sanas
// salían marcadas sólo por el nombre del hueco — medido.
func englishHitsIn(value string) []string {
	value = i18nVarHole.ReplaceAllString(value, " ")
	var hits []string
	for _, w := range i18nWordPattern.FindAllString(strings.ToLower(value), -1) {
		if englishFunctionWords[w] {
			hits = append(hits, w)
		}
	}
	sort.Strings(hits)
	return hits
}

// TestNoEnglishFunctionWordsInSpanish: mira SÓLO esCatalog — es el único
// mapa donde una palabra función inglesa es un síntoma.
func TestNoEnglishFunctionWordsInSpanish(t *testing.T) {
	for key, es := range esCatalog {
		hits := englishHitsIn(es)
		if len(hits) == 0 {
			continue
		}
		if _, allowed := englishInSpanishExceptions[key]; allowed {
			continue
		}
		t.Errorf("%s tiene palabras inglesas en el catálogo ESPAÑOL (%v) en %q — si el texto quedó "+
			"sin traducir, tradúcelo; si esa palabra está ahí a propósito, agrega LA CLAVE a "+
			"englishInSpanishExceptions con el motivo (no saques la palabra de "+
			"englishFunctionWords: apagaría la señal para todo el catálogo)", key, hits, es)
	}
}

// TestEnglishInSpanishExceptionsAreAlive: la misma honestidad que pide A.
// Una excepción cuya clave ya no tiene ninguna palabra inglesa es un permiso
// olvidado, y taparía un defecto futuro con ese mismo nombre.
func TestEnglishInSpanishExceptionsAreAlive(t *testing.T) {
	for key, reason := range englishInSpanishExceptions {
		es, ok := esCatalog[key]
		if !ok {
			t.Errorf("englishInSpanishExceptions tiene %q (%s) pero esa clave ya no está en "+
				"esCatalog — borra la excepción", key, reason)
			continue
		}
		if len(englishHitsIn(es)) == 0 {
			t.Errorf("englishInSpanishExceptions tiene %q (%s) pero su valor ya no tiene ninguna "+
				"palabra inglesa (%q) — la excepción sobra, borrala", key, reason, es)
		}
	}
}
