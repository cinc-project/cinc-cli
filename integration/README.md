# Integration tests

End-to-end tests of the `cinc` binary against real server implementations.
This directory is its own Go module, so the cinc-server-ng test dependency
never reaches the root module. The binary under test is still built from the
root module, exactly as a release build is.

| Package | What it is | When it runs |
|---|---|---|
| `suite/` | Every case, written once against a `suite.Target`, plus the coverage guard | Coverage guard on every `go test ./...` here |
| `cincserverng/` | Runs the suite against an in-process [cinc-server-ng](https://github.com/cinc-project/cinc-server-ng) with signature checks and ACL enforcement on | Every pull request, every push to `main`, and every release tag before its binaries are published |
| `cincservererlang/` | Runs the suite against the CINC Server Erlang stack (erchef) in AWS | By hand, with `run-cinc-server-erlang.sh` |
| `terraform/` | The AWS stack for `cincservererlang` | By hand, through the script |

```sh
make test-integration            # from the repository root
cd integration && go test ./...  # the same thing
go test ./cincserverng/ -run 'TestCincServerNG/nodes/'   # one family
```

Set `CINC_BIN` to run the suite against an already-built binary (a release
archive, say) instead of building one.

## Writing a case

Each family (nodes, roles, cookbooks, ...) lives in its own file in `suite/`
and lists its cases with the leaf commands each one covers. A case gets a
`cli` that runs the binary in a throwaway `HOME` whose credentials sign as the
target's admin, and:

- names every object it creates with `uniqueName`, and registers a
  `c.cleanup(...)` delete for it, so cases run in parallel and reruns against
  a long-lived server never collide;
- depends on no seed data and no other case;
- asserts on `--format json` output where the command offers it;
- wraps anything that reads through search in `eventually`, since erchef
  indexes asynchronously.

`TestCoverage` walks the live command tree and fails if a leaf command is
neither covered by a case nor listed, with a reason, in `exempt`
(`suite/coverage.go`). A family can list a command it does not cover yet in
`pending`, which the guard accepts until a case claims it.

## Running against cinc-server-erlang

Needs `terraform`, `go`, and AWS credentials in the environment or an AWS
profile (`AWS_PROFILE=...`). Nothing secret is committed: the admin key and
the test CA are generated on your machine and kept in local, gitignored
Terraform state and `terraform/.out/`.

```sh
./run-cinc-server-erlang.sh            # apply, test, destroy (always)
KEEP=1 ./run-cinc-server-erlang.sh     # apply and test; leave the stack up
./run-cinc-server-erlang.sh destroy    # tear down a kept stack
```

With a kept stack, re-run the tests directly, or narrow a run:

```sh
cd cincservererlang && go test -count=1 -v ./...
GOTESTFLAGS='-run TestCincServerErlang/cookbooks/' KEEP=1 ./run-cinc-server-erlang.sh
```

`go test` reads `terraform/.out/target.json`, or the file named by
`CINC_SERVER_ERLANG_TARGET`. Without it the package skips.

Any change to a request the CLI sends should be run here before it merges:
cinc-server-ng accepting a request does not mean erchef will.

### What the stack creates

In `us-west-2` by default (`-var region=...`):

- A VPC with one public subnet, an internet gateway and a route table.
- A security group allowing HTTPS (443) from your current public IP only
  (`-var allowed_cidr=...` to change). No SSH.
- One `t3.xlarge` Ubuntu 22.04 instance with an Elastic IP, an encrypted 50 GB
  root volume, IMDSv2, and an instance profile for SSM Session Manager.
- CINC Server `15.10.125` from the stable channel, installed from the package
  omnitruck lists and checked against its SHA-256. The bootstrap creates the
  orgs `cinccli` and `cinccli-other` and the admin `cinccli-admin` (an admin
  of both and a server-admin) authenticating with the Terraform-generated key.

TLS is verified: Terraform generates a throwaway CA and a certificate for the
Elastic IP, and each case's credentials trust only that CA through the default
`trusted_certs_dir`. The certificate's key reaches the instance through
user-data, so anyone in the account who can read the instance's user-data can
see it; it protects only this short-lived server.

The instance shuts itself down (and, with `terminate` shutdown behaviour, is
terminated) four hours after boot, so a forgotten stack stops billing for
compute. The Elastic IP and VPC remain until `destroy`.

Everything the stack creates is tagged `cinc-cli-integration=true`. After
every destroy the script lists anything still carrying that tag (when the AWS
CLI is installed): Terraform removes only what its state tracks. Delete such
leftovers by hand, e.g. `aws ec2 release-address --allocation-id ...` and
`aws ec2 delete-vpc --vpc-id ...`.

### Cost and time

About $0.17/hour for the `t3.xlarge` plus the Elastic IP. A run takes about
10 minutes: roughly 1 for `apply`, 5 for the package install and
`reconfigure`, a few for the suite, and 1 for `destroy`.

### Debugging

```sh
aws ssm start-session --target "$(terraform -chdir=terraform output -raw instance_id)"
sudo tail -f /var/log/cloud-init-output.log   # the bootstrap
sudo cinc-server-ctl status
```

`/etc/cinc-cli-it/ready` exists once the bootstrap has finished.

## Gaps

A case one server cannot pass is listed in that target's `Gaps` with the
reason. On cinc-server-ng every gap links an upstream issue and is removed
once the issue is fixed. On cinc-server-erlang a gap is only for a server
limitation, such as an operation erchef reserves to the pivotal superuser
(the tests' admin is a server-admin, and pivotal's key never leaves the
instance), and states what was observed. `suite.Run` rejects a gap that names no case or has
no reason.
