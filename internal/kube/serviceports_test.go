package kube

import "testing"

// Another app in the environment reaches this one the way Compose does, by
// name and port: db:5432. The Service used to listen on 80 alone.
func TestAServiceListensOnTheAppsOwnPortToo(t *testing.T) {
	service := BuildService(AppSpec{Name: "db", Namespace: "acme-shop-production", Port: 5432})
	if service == nil || len(service.Spec.Ports) != 2 {
		t.Fatalf("ports %+v", service)
	}
	if http, app := service.Spec.Ports[0], service.Spec.Ports[1]; http.Port != 80 || app.Port != 5432 ||
		app.TargetPort.StrVal != "http" {
		t.Fatalf("ports %+v", service.Spec.Ports)
	}
	// An app on 80 needs it once.
	if web := BuildService(AppSpec{Name: "web", Namespace: "n", Port: 80}); len(web.Spec.Ports) != 1 {
		t.Fatalf("an app on 80 has ports %+v", web.Spec.Ports)
	}
}
