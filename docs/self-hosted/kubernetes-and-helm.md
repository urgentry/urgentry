# Kubernetes and Helm

Urgentry ships both raw Kubernetes manifests and a Helm chart for self-hosted mode.

## Raw manifests

The kustomize bundle lives under `deploy/k8s`.

```bash
kubectl apply -k deploy/k8s
kubectl -n urgentry-system get pods
```

Before applying the bundle, replace the placeholder values in `deploy/k8s/secret.yaml` or layer in your own secret management.

Smoke the deployment:

```bash
bash deploy/k8s/smoke.sh up
```

On a local `kind-*` context, `deploy/k8s/smoke.sh up` now builds and loads `urgentry:dev`, generates a temporary secret when `deploy/k8s/secret.yaml` still contains `REPLACE_ME_*` values, rewrites the namespace override it advertises, and provisions a temporary RWX hostPath PV for `/data`. That makes the smoke path self-contained on a clean local cluster without mutating the checked-in manifest. For a real cluster, continue managing `deploy/k8s/secret.yaml` through your normal secret flow before `kubectl apply -k deploy/k8s`.

## Helm

The Helm chart lives under `deploy/helm/urgentry`.

```bash
helm upgrade --install urgentry deploy/helm/urgentry \
  --set postgresql.password=changeme \
  --set bootstrap.password=changeme
```

Use Helm when you already manage application configuration and secrets through Kubernetes tooling. Use the raw manifests when you want the smallest explicit baseline.

`deploy/helm/urgentry/smoke.sh up` follows the same local-cluster assumptions: it builds and loads `urgentry:dev` on `kind-*` contexts, generates temporary dependency secrets, and isolates its temporary RWX PV path by namespace and release so repeated validation runs do not collide with each other.
