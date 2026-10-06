package env

import "testing"

func TestFakeRuntimeSecretsStayOutOfWorkspace(t *testing.T) {
	f := &FakeRuntime{}
	id, err := f.CreateAndStart(Spec{Name: "box", Image: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.PutSecret(id, "npm_token", "sekrit"); err != nil {
		t.Fatal(err)
	}
	if err := f.PutSecret(id, "../x", "no"); err == nil {
		t.Fatal("escaped id")
	}
	got, ok := f.LookupSecret(id, "npm_token")
	if !ok || got != "sekrit" {
		t.Fatalf("%q %v", got, ok)
	}
	if f.HasFile(id, SecretDir+"/npm_token") {
		t.Fatal("secret visible as workspace file")
	}
	if _, err := f.ReadFile(id, SecretDir+"/npm_token"); err == nil {
		t.Fatal("secret readable as workspace file")
	}
	if err := f.DeleteSecrets(id); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.LookupSecret(id, "npm_token"); ok {
		t.Fatal("secret survived delete")
	}
}
