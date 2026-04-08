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

## Helm

The Helm chart lives under `deploy/helm/urgentry`.

```bash
helm upgrade --install urgentry deploy/helm/urgentry \
  --set postgresql.password=changeme \
  --set bootstrap.password=changeme
```

Use Helm when you already manage application configuration and secrets through Kubernetes tooling. Use the raw manifests when you want the smallest explicit baseline.
