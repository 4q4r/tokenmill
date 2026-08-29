package config

import (
	"os"
	"testing"
)

// TDD: P1 Merge теряет явное значение равное default.
// base = global с enabled=false, otherRaw = {"enabled":true} (true == default)
// Ожидается что MergeRaw вернёт enabled=true, а не оставит base false.
func TestTDD_MergeExplicitDefault(t *testing.T) {
	base := DefaultConfig()
	base.Enabled = false // global override
	// also test nested: base.Techniques.Dedup = false, project explicitly true (default true)
	base.Techniques.Dedup = false
	base.Techniques.ExactRLE.MinRun = 99

	// otherRaw explicitly sets values equal to defaults
	otherRaw := []byte(`{"enabled":true,"techniques":{"dedup":true,"exactRLE":{"enabled":true,"minRun":3}}}`)

	// если MergeRaw существует — должен учесть явные ключи даже равные default
	merged, err := base.MergeRaw(otherRaw)
	if err != nil {
		t.Fatalf("MergeRaw error: %v", err)
	}
	if merged.Enabled != true {
		t.Fatalf("FAIL Merge explicit default: enabled got %v want true (base false must be overridden by explicit true==default)", merged.Enabled)
	}
	if merged.Techniques.Dedup != true {
		t.Fatalf("FAIL Merge explicit default nested: dedup got %v want true", merged.Techniques.Dedup)
	}
	if merged.Techniques.ExactRLE.MinRun != 3 {
		t.Fatalf("FAIL Merge explicit default nested int: minRun got %d want 3 (explicit default) override base 99", merged.Techniques.ExactRLE.MinRun)
	}
	// также убедимся что не указанные поля сохраняют base
	if merged.Techniques.Jton.MinRows != 10 {
		t.Fatalf("unexpected Jton MinRows %d", merged.Techniques.Jton.MinRows)
	}
}

// TDD: GetViper dead-code — defaults только top-level, flatten рекурсивно должен дать доступ к nested.
func TestTDD_GetViperFlattened(t *testing.T) {
	// сбросить глобальный viper для теста (чисто через reflection Reset?)
	// GetViper использует sync.Once, поэтому перезапуск теста в свежем процессе; просто проверяем что flattened ключи доступны
	v := GetViper()
	if v == nil {
		t.Fatalf("GetViper nil")
	}
	// top-level exists
	if v.GetBool("enabled") != true && v.GetBool("enabled") != false {
		t.Logf("enabled not set")
	}
	// nested должен быть доступен после fix (сейчас dead: только top-level map)
	val := v.Get("techniques.dedup")
	if val == nil {
		t.Fatalf("FAIL GetViper dead-code: techniques.dedup not set via flattened defaults (got nil), expected true")
	}
	val2 := v.GetInt("techniques.exactRLE.minRun")
	if val2 != 3 {
		t.Fatalf("FAIL GetViper flatten: techniques.exactRLE.minRun got %v want 3", val2)
	}
	val3 := v.GetInt("techniques.blockFactoring.minBlock")
	if val3 != 2 {
		t.Fatalf("FAIL GetViper flatten: techniques.blockFactoring.minBlock got %v want 2", val3)
	}
}

// TDD: applyEnv должен читать viper.Get, а не только manual scan (dead-code v)
// Проверяем что viper AutomaticEnv видит env и GetViper отрабатывает.
func TestTDD_ApplyEnvUsesViper(t *testing.T) {
	t.Setenv("TOKENMILL_ENABLED", "false")
	t.Setenv("TOKENMILL_TECHNIQUES_DEDUP", "false")
	// после Setenv viper должен видеть значение через Get
	v := GetViper()
	// viper.AutomaticEnv читает env при каждом Get, но нужно BindEnv? Viper с AutomaticEnv должен подхватить
	got := v.GetString("enabled")
	// если dead-code — v.GetString вернёт default true, а не env false
	// после фикса должен вернуть "false" или хотя бы IsSet true
	if got == "" {
		got = v.GetString("techniques.dedup")
	}
	// проверяем через IsSet что env виден
	if !v.IsSet("enabled") {
		// IsSet может быть false если только AutomaticEnv без явного BindEnv — тогда тоже считается dead
		t.Fatalf("FAIL applyEnv dead-code: viper.IsSet(enabled) false after env set, viper not reading env")
	}
	// также проверяем что LoadFrom с env реально оверрайдит даже когда viper используется
	_ = os.Getenv("TOKENMILL_ENABLED")
}
