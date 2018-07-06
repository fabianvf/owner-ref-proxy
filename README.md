Usage
-----

First get the project built:
```bash
dep ensure
go build
```

Then you can start the proxy like this (using a specific service account as an example, you'll need to tweak those values):

```bash
./owner-ref-proxy --port 8001
```

Then create a new kubeconfig with a base64 encoded JSON object as the user field for BasicAuth in the cluster URL. The JSON object should have the fields `apiVersion`, `kind`, `name`, `uid`. For example, for the default serviceaccount in your current namespace, the following command would create the proper kubeconfig:

```bash
cat <<EOF >config
apiVersion: v1
kind: Config
clusters:
- cluster:
    insecure-skip-tls-verify: true
    server: http://$(echo '{"name": "default", "kind": "ServiceAccount", "apiVersion": "v1", "uid": "'$(kubectl get sa default -o yaml | grep uid | awk '{print $2}')'"}'  | base64 -w0)@127.0.0.1:8001                
  name: 127-0-0-1:8001
contexts:
- context:
    cluster: 127-0-0-1:8001
    user: admin/127-0-0-1:8001
  name: test/127-0-0-1:8001/admin:test
current-context: test/127-0-0-1:8001/admin:test
preferences: {}
users:
- name: admin/127-0-0-1:8001
EOF
```

You should then be able to run kubectl commands with the `--kubeconfig config` argument as normal. Any resources that are created by those commands will have the specified ownerReference injected into them.
