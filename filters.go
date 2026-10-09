package copier

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"math"
	"math/rand"
	"path"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/flosch/pongo2/v6"
	"gopkg.in/yaml.v3"
)

func init() {
	// Jinja2 / jinja2-ansible-filters style filters that Copier templates commonly use.
	// pongo2 filters accept a single argument using the `|filter:arg` syntax.
	mustRegister("to_nice_yaml", filterToNiceYAML)
	mustRegister("to_yaml", filterToYAML)
	mustRegister("from_yaml", filterFromYAML)
	mustRegister("to_json", filterToJSON)
	mustRegister("tojson", filterToJSON)
	mustRegister("to_nice_json", filterToNiceJSON)
	mustRegister("from_json", filterFromJSON)
	mustRegister("bool", filterBool)
	mustRegister("int", filterInt)
	mustRegister("string", filterString)
	mustRegister("str", filterString)
	mustRegister("list", filterList)
	mustRegister("basename", filterBasename)
	mustRegister("dirname", filterDirname)
	mustRegister("trim", filterTrim)
	mustRegister("capitalize", filterCapitalize)
	mustRegister("indent", filterIndent)
	mustRegister("b64encode", filterB64Encode)
	mustRegister("b64decode", filterB64Decode)
	mustRegister("hash", filterHash)
	mustRegister("md5", filterMD5)
	mustRegister("sha1", filterSHA1)
	mustRegister("checksum", filterSHA1)
	mustRegister("sha256", filterSHA256)
	mustRegister("strftime", filterStrftime)
	mustRegister("quote", filterQuote)
	mustRegister("type_debug", filterTypeDebug)
	mustRegister("mandatory", filterMandatory)
	mustRegister("unique", filterUnique)
	mustRegister("sort", filterSort)
	mustRegister("reverse", filterReverse)
	mustRegister("min", filterMin)
	mustRegister("max", filterMax)
	mustRegister("sum", filterSum)
	mustRegister("abs", filterAbs)
	mustRegister("round", filterRound)
	mustRegister("flatten", filterFlatten)
	mustRegister("dict2items", filterDict2Items)
	mustRegister("items2dict", filterItems2Dict)
	mustRegister("combine", filterCombine)
	mustRegister("difference", filterDifference)
	mustRegister("intersect", filterIntersect)
	mustRegister("union", filterUnion)
	mustRegister("keys", filterKeys)
	mustRegister("values", filterValues)
	mustRegister("regex_search", filterRegexSearch)
	mustRegister("regex_findall", filterRegexFindall)
	mustRegister("regex_escape", filterRegexEscape)
	mustRegister("ans_random", filterAnsRandom)
	mustRegister("splitext", filterSplitext)
}

func mustRegister(name string, fn pongo2.FilterFunction) {
	if err := pongo2.RegisterFilter(name, fn); err != nil {
		// Filter already registered — safe to ignore on re-init.
		_ = err
	}
}

func filterErr(sender string, err error) *pongo2.Error {
	return &pongo2.Error{Sender: "filter:" + sender, OrigError: err}
}

func filterToNiceYAML(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	out, err := marshalNiceYAML(in.Interface())
	if err != nil {
		return nil, filterErr("to_nice_yaml", err)
	}
	return pongo2.AsSafeValue(out), nil
}

func filterToYAML(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	data, err := yaml.Marshal(in.Interface())
	if err != nil {
		return nil, filterErr("to_yaml", err)
	}
	return pongo2.AsSafeValue(string(data)), nil
}

func filterFromYAML(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	var out any
	if err := yaml.Unmarshal([]byte(in.String()), &out); err != nil {
		return nil, filterErr("from_yaml", err)
	}
	return pongo2.AsValue(out), nil
}

func filterToJSON(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	data, err := json.Marshal(jsonCompatible(in.Interface()))
	if err != nil {
		return nil, filterErr("to_json", err)
	}
	return pongo2.AsSafeValue(string(data)), nil
}

func filterToNiceJSON(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	indent := 4
	if param != nil && !param.IsNil() && param.IsInteger() {
		indent = param.Integer()
	}
	data, err := json.MarshalIndent(jsonCompatible(in.Interface()), "", strings.Repeat(" ", indent))
	if err != nil {
		return nil, filterErr("to_nice_json", err)
	}
	return pongo2.AsSafeValue(string(data)), nil
}

func filterFromJSON(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	var out any
	if err := json.Unmarshal([]byte(in.String()), &out); err != nil {
		return nil, filterErr("from_json", err)
	}
	return pongo2.AsValue(out), nil
}

// jsonCompatible converts map[any]any values (from YAML) into map[string]any.
func jsonCompatible(v any) any {
	switch x := v.(type) {
	case map[any]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[fmt.Sprintf("%v", k)] = jsonCompatible(val)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = jsonCompatible(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = jsonCompatible(val)
		}
		return out
	case func() string:
		return x()
	}
	return v
}

func filterBool(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	return pongo2.AsValue(castToBool(in.Interface())), nil
}

func filterInt(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	n, err := castToInt(in.Interface())
	if err != nil {
		if s, ok := in.Interface().(string); ok {
			if f, ferr := castToFloat(s); ferr == nil {
				return pongo2.AsValue(int64(f)), nil
			}
		}
		if param != nil && !param.IsNil() {
			return param, nil
		}
		return pongo2.AsValue(0), nil
	}
	return pongo2.AsValue(n), nil
}

func filterString(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	return pongo2.AsValue(in.String()), nil
}

func filterList(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	return pongo2.AsValue(iterableValues(in.Interface())), nil
}

func filterBasename(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	return pongo2.AsValue(path.Base(in.String())), nil
}

func filterDirname(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	d := path.Dir(in.String())
	if d == "." && !strings.Contains(in.String(), "/") {
		d = ""
	}
	return pongo2.AsValue(d), nil
}

func filterSplitext(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	s := in.String()
	ext := path.Ext(s)
	return pongo2.AsValue([]any{strings.TrimSuffix(s, ext), ext}), nil
}

func filterTrim(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	if param != nil && !param.IsNil() {
		return pongo2.AsValue(strings.Trim(in.String(), param.String())), nil
	}
	return pongo2.AsValue(strings.TrimSpace(in.String())), nil
}

func filterCapitalize(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	s := in.String()
	if s == "" {
		return in, nil
	}
	return pongo2.AsValue(strings.ToUpper(s[:1]) + strings.ToLower(s[1:])), nil
}

func filterIndent(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	width := 4
	if param != nil && !param.IsNil() && param.IsInteger() {
		width = param.Integer()
	}
	pad := strings.Repeat(" ", width)
	lines := strings.Split(in.String(), "\n")
	for i := 1; i < len(lines); i++ {
		if lines[i] != "" {
			lines[i] = pad + lines[i]
		}
	}
	return pongo2.AsValue(strings.Join(lines, "\n")), nil
}

func filterB64Encode(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	return pongo2.AsValue(base64.StdEncoding.EncodeToString([]byte(in.String()))), nil
}

func filterB64Decode(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	data, err := base64.StdEncoding.DecodeString(in.String())
	if err != nil {
		return nil, filterErr("b64decode", err)
	}
	return pongo2.AsValue(string(data)), nil
}

func hashHex(h hash.Hash, s string) string {
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

func filterHash(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	algo := "sha1"
	if param != nil && !param.IsNil() {
		algo = strings.ToLower(param.String())
	}
	switch algo {
	case "md5":
		return pongo2.AsValue(hashHex(md5.New(), in.String())), nil
	case "sha1":
		return pongo2.AsValue(hashHex(sha1.New(), in.String())), nil
	case "sha256":
		return pongo2.AsValue(hashHex(sha256.New(), in.String())), nil
	case "sha512":
		return pongo2.AsValue(hashHex(sha512.New(), in.String())), nil
	}
	return nil, filterErr("hash", fmt.Errorf("unsupported hash algorithm %q", algo))
}

func filterMD5(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	return pongo2.AsValue(hashHex(md5.New(), in.String())), nil
}

func filterSHA1(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	return pongo2.AsValue(hashHex(sha1.New(), in.String())), nil
}

func filterSHA256(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	return pongo2.AsValue(hashHex(sha256.New(), in.String())), nil
}

// filterStrftime formats the current time (or the unix timestamp given as
// argument) with a Python strftime format, e.g. `{{ '%Y-%m-%d' | strftime }}`.
func filterStrftime(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	t := time.Now()
	if param != nil && !param.IsNil() && param.IsNumber() {
		t = time.Unix(int64(param.Float()), 0)
	}
	return pongo2.AsValue(strftime(t, in.String())), nil
}

func strftime(t time.Time, format string) string {
	var b strings.Builder
	for i := 0; i < len(format); i++ {
		c := format[i]
		if c != '%' || i+1 >= len(format) {
			b.WriteByte(c)
			continue
		}
		i++
		switch format[i] {
		case 'Y':
			fmt.Fprintf(&b, "%04d", t.Year())
		case 'y':
			fmt.Fprintf(&b, "%02d", t.Year()%100)
		case 'm':
			fmt.Fprintf(&b, "%02d", int(t.Month()))
		case 'd':
			fmt.Fprintf(&b, "%02d", t.Day())
		case 'H':
			fmt.Fprintf(&b, "%02d", t.Hour())
		case 'I':
			h := t.Hour() % 12
			if h == 0 {
				h = 12
			}
			fmt.Fprintf(&b, "%02d", h)
		case 'M':
			fmt.Fprintf(&b, "%02d", t.Minute())
		case 'S':
			fmt.Fprintf(&b, "%02d", t.Second())
		case 'f':
			fmt.Fprintf(&b, "%06d", t.Nanosecond()/1000)
		case 'p':
			if t.Hour() < 12 {
				b.WriteString("AM")
			} else {
				b.WriteString("PM")
			}
		case 'b', 'h':
			b.WriteString(t.Format("Jan"))
		case 'B':
			b.WriteString(t.Format("January"))
		case 'a':
			b.WriteString(t.Format("Mon"))
		case 'A':
			b.WriteString(t.Format("Monday"))
		case 'j':
			fmt.Fprintf(&b, "%03d", t.YearDay())
		case 'z':
			b.WriteString(t.Format("-0700"))
		case 'Z':
			b.WriteString(t.Format("MST"))
		case 'w':
			fmt.Fprintf(&b, "%d", int(t.Weekday()))
		case 'U', 'W':
			_, week := t.ISOWeek()
			fmt.Fprintf(&b, "%02d", week)
		case 'c':
			b.WriteString(t.Format("Mon Jan _2 15:04:05 2006"))
		case 'x':
			b.WriteString(t.Format("01/02/06"))
		case 'X':
			b.WriteString(t.Format("15:04:05"))
		case 's':
			fmt.Fprintf(&b, "%d", t.Unix())
		case '%':
			b.WriteByte('%')
		default:
			b.WriteByte('%')
			b.WriteByte(format[i])
		}
	}
	return b.String()
}

func filterQuote(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	s := in.String()
	if s == "" {
		return pongo2.AsValue("''"), nil
	}
	if !strings.ContainsAny(s, " \t\n\"'`$&|;<>()*?[]{}\\!#~") {
		return pongo2.AsValue(s), nil
	}
	return pongo2.AsValue("'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"), nil
}

func filterTypeDebug(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	v := in.Interface()
	switch v.(type) {
	case nil:
		return pongo2.AsValue("NoneType"), nil
	case string:
		return pongo2.AsValue("str"), nil
	case bool:
		return pongo2.AsValue("bool"), nil
	case int, int64, int32:
		return pongo2.AsValue("int"), nil
	case float64, float32:
		return pongo2.AsValue("float"), nil
	}
	switch reflect.ValueOf(v).Kind() {
	case reflect.Slice, reflect.Array:
		return pongo2.AsValue("list"), nil
	case reflect.Map:
		return pongo2.AsValue("dict"), nil
	}
	return pongo2.AsValue(fmt.Sprintf("%T", v)), nil
}

func filterMandatory(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	if in.IsNil() || (in.IsString() && in.String() == "") {
		msg := "Mandatory variable not defined."
		if param != nil && !param.IsNil() {
			msg = param.String()
		}
		return nil, filterErr("mandatory", fmt.Errorf("%s", msg))
	}
	return in, nil
}

func filterUnique(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	var out []any
	seen := map[string]bool{}
	for _, v := range iterableValues(in.Interface()) {
		k := fmt.Sprintf("%T:%v", v, v)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, v)
	}
	return pongo2.AsValue(out), nil
}

func lessAny(a, b any) bool {
	fa, okA := toFloat(a)
	fb, okB := toFloat(b)
	if okA && okB {
		return fa < fb
	}
	return fmt.Sprintf("%v", a) < fmt.Sprintf("%v", b)
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case int32:
		return float64(x), true
	case float64:
		return x, true
	case float32:
		return float64(x), true
	}
	return 0, false
}

func filterSort(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	items := iterableValues(in.Interface())
	sort.SliceStable(items, func(i, j int) bool { return lessAny(items[i], items[j]) })
	if param != nil && !param.IsNil() && param.IsTrue() {
		for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
			items[i], items[j] = items[j], items[i]
		}
	}
	return pongo2.AsValue(items), nil
}

func filterReverse(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	if in.IsString() {
		r := []rune(in.String())
		for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
			r[i], r[j] = r[j], r[i]
		}
		return pongo2.AsValue(string(r)), nil
	}
	items := iterableValues(in.Interface())
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	return pongo2.AsValue(items), nil
}

func filterMin(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	items := iterableValues(in.Interface())
	if len(items) == 0 {
		return pongo2.AsValue(nil), nil
	}
	best := items[0]
	for _, v := range items[1:] {
		if lessAny(v, best) {
			best = v
		}
	}
	return pongo2.AsValue(best), nil
}

func filterMax(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	items := iterableValues(in.Interface())
	if len(items) == 0 {
		return pongo2.AsValue(nil), nil
	}
	best := items[0]
	for _, v := range items[1:] {
		if lessAny(best, v) {
			best = v
		}
	}
	return pongo2.AsValue(best), nil
}

func filterSum(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	var total float64
	allInt := true
	for _, v := range iterableValues(in.Interface()) {
		f, ok := toFloat(v)
		if !ok {
			return nil, filterErr("sum", fmt.Errorf("cannot sum non-numeric value %v", v))
		}
		if _, isFloat := v.(float64); isFloat {
			allInt = false
		}
		total += f
	}
	if allInt {
		return pongo2.AsValue(int64(total)), nil
	}
	return pongo2.AsValue(total), nil
}

func filterAbs(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	if in.IsInteger() {
		n := in.Integer()
		if n < 0 {
			n = -n
		}
		return pongo2.AsValue(n), nil
	}
	return pongo2.AsValue(math.Abs(in.Float())), nil
}

func filterRound(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	precision := 0
	if param != nil && !param.IsNil() && param.IsInteger() {
		precision = param.Integer()
	}
	factor := math.Pow(10, float64(precision))
	return pongo2.AsValue(math.Round(in.Float()*factor) / factor), nil
}

func filterFlatten(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	var out []any
	var walk func(v any)
	walk = func(v any) {
		rv := reflect.ValueOf(v)
		if v != nil && (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array) {
			for i := 0; i < rv.Len(); i++ {
				walk(rv.Index(i).Interface())
			}
			return
		}
		if v != nil {
			out = append(out, v)
		}
	}
	walk(in.Interface())
	return pongo2.AsValue(out), nil
}

func toStringMap(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case map[string]any:
		return m, true
	case map[any]any:
		out := make(map[string]any, len(m))
		for k, val := range m {
			out[fmt.Sprintf("%v", k)] = val
		}
		return out, true
	case map[string]string:
		out := make(map[string]any, len(m))
		for k, val := range m {
			out[k] = val
		}
		return out, true
	}
	return nil, false
}

func sortedMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func filterDict2Items(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	m, ok := toStringMap(in.Interface())
	if !ok {
		return nil, filterErr("dict2items", fmt.Errorf("requires a dictionary"))
	}
	out := make([]any, 0, len(m))
	for _, k := range sortedMapKeys(m) {
		out = append(out, map[string]any{"key": k, "value": m[k]})
	}
	return pongo2.AsValue(out), nil
}

func filterItems2Dict(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	out := map[string]any{}
	for _, item := range iterableValues(in.Interface()) {
		m, ok := toStringMap(item)
		if !ok {
			return nil, filterErr("items2dict", fmt.Errorf("requires a list of key/value dictionaries"))
		}
		out[fmt.Sprintf("%v", m["key"])] = m["value"]
	}
	return pongo2.AsValue(out), nil
}

func filterCombine(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	base, ok := toStringMap(in.Interface())
	if !ok {
		return nil, filterErr("combine", fmt.Errorf("requires dictionaries"))
	}
	out := make(map[string]any, len(base))
	for k, v := range base {
		out[k] = v
	}
	if param != nil && !param.IsNil() {
		other, ok := toStringMap(param.Interface())
		if !ok {
			return nil, filterErr("combine", fmt.Errorf("requires dictionaries"))
		}
		for k, v := range other {
			out[k] = v
		}
	}
	return pongo2.AsValue(out), nil
}

func setOp(in, param *pongo2.Value, keep func(inOther bool) bool, includeOther bool) *pongo2.Value {
	var other []any
	if param != nil && !param.IsNil() {
		other = iterableValues(param.Interface())
	}
	otherSet := map[string]bool{}
	for _, v := range other {
		otherSet[fmt.Sprintf("%T:%v", v, v)] = true
	}
	var out []any
	seen := map[string]bool{}
	for _, v := range iterableValues(in.Interface()) {
		k := fmt.Sprintf("%T:%v", v, v)
		if seen[k] || !keep(otherSet[k]) {
			continue
		}
		seen[k] = true
		out = append(out, v)
	}
	if includeOther {
		for _, v := range other {
			k := fmt.Sprintf("%T:%v", v, v)
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, v)
		}
	}
	return pongo2.AsValue(out)
}

func filterDifference(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	return setOp(in, param, func(inOther bool) bool { return !inOther }, false), nil
}

func filterIntersect(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	return setOp(in, param, func(inOther bool) bool { return inOther }, false), nil
}

func filterUnion(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	return setOp(in, param, func(bool) bool { return true }, true), nil
}

func filterKeys(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	m, ok := toStringMap(in.Interface())
	if !ok {
		return nil, filterErr("keys", fmt.Errorf("requires a dictionary"))
	}
	keys := sortedMapKeys(m)
	out := make([]any, len(keys))
	for i, k := range keys {
		out[i] = k
	}
	return pongo2.AsValue(out), nil
}

func filterValues(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	m, ok := toStringMap(in.Interface())
	if !ok {
		return nil, filterErr("values", fmt.Errorf("requires a dictionary"))
	}
	out := make([]any, 0, len(m))
	for _, k := range sortedMapKeys(m) {
		out = append(out, m[k])
	}
	return pongo2.AsValue(out), nil
}

func filterRegexSearch(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	if param == nil || param.IsNil() {
		return nil, filterErr("regex_search", fmt.Errorf("requires a pattern"))
	}
	re, err := regexp.Compile(param.String())
	if err != nil {
		return nil, filterErr("regex_search", err)
	}
	m := re.FindStringSubmatch(in.String())
	if m == nil {
		return pongo2.AsValue(nil), nil
	}
	if len(m) > 1 {
		groups := make([]any, len(m)-1)
		for i, g := range m[1:] {
			groups[i] = g
		}
		return pongo2.AsValue(groups), nil
	}
	return pongo2.AsValue(m[0]), nil
}

func filterRegexFindall(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	if param == nil || param.IsNil() {
		return nil, filterErr("regex_findall", fmt.Errorf("requires a pattern"))
	}
	re, err := regexp.Compile(param.String())
	if err != nil {
		return nil, filterErr("regex_findall", err)
	}
	matches := re.FindAllString(in.String(), -1)
	out := make([]any, len(matches))
	for i, m := range matches {
		out[i] = m
	}
	return pongo2.AsValue(out), nil
}

func filterRegexEscape(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	return pongo2.AsValue(regexp.QuoteMeta(in.String())), nil
}

func filterAnsRandom(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	if in.IsNumber() {
		n := int64(in.Float())
		if n <= 0 {
			return pongo2.AsValue(0), nil
		}
		return pongo2.AsValue(rand.Int63n(n)), nil //nolint:gosec // Not for security.
	}
	items := iterableValues(in.Interface())
	if len(items) == 0 {
		return pongo2.AsValue(nil), nil
	}
	return pongo2.AsValue(items[rand.Intn(len(items))]), nil //nolint:gosec // Not for security.
}
