Usage
-----

First get the project built:
```bash
dep ensure
go build
```

Then you can start the proxy like this (using a specific service account as an example, you'll need to tweak those values):

```bash
./owner-ref-proxy --owner-name default --owner-kind ServiceAccount --owner-api-version v1 --owner-uid 79bc811c-7a29-11e8-94e7-64006a7fd6c0 --port 8001
```

Then create a new kubeconfig, that looks like this:
```yaml
apiVersion: v1
clusters:
- cluster:
    insecure-skip-tls-verify: true
    server: http://127.0.0.1:8001
  name: 127.0.0.1:8001
contexts:
- context:
    cluster: 127.0.0.1:8001
    user: admin/127.0.0.1:8001
  name: 127.0.0.1:8001
current-context: 127.0.0.1:8001
kind: Config
preferences: {}
users:
- name: admin/127.0.0.1:8001
```

You should then be able to run kubectl commands with the `--kubeconfig <file>` argument as normal. Any resources that are created by those commands will have the specified ownerReference injected into them.
