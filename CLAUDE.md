# Selfcloud

A self-hosted drive, written in Go, as generic as possible. It's a self-contained product with
its own users, sign-in, storage and deployment, and it depends on no other system. It is not
built for any one product: any application can use it through its API. Nothing is built yet.
Propose the design to Shuki before building.

Internal background (why it exists, server facts, open questions) is in `CLAUDE.local.md`, which
git ignores. This repository is meant to be public, so keep internal hostnames, IP addresses,
customer details, other products' names and secrets out of everything that gets committed.

## Requirements

1. **Self-contained and generic.** Selfcloud owns its users, data and pages, and assumes nothing
   about the applications that use it.
2. **Two ways to run:**
   - a stand-alone headless server, API only;
   - the same server with its own web interface.
3. **API.** Other applications manage Selfcloud and work with its files through the API: create
   and verify users, attach folders, upload, download.
4. **Users and folders.** An account owner creates users. Each user must be verified before
   they can sign in. The owner attaches one or more folders to each user.
5. **Very large files.** The target is 100 GB per file. Uploads go in pieces and resume after
   a dropped connection; downloads resume too. A proxy in front may cap each request (Cloudflare
   caps it at 100 MB, for example), so the piece size must be settable.
6. **Fast.** The file system should be reached by the fastest method available.
7. **Docker.** It runs in Docker as well as directly on a host.
8. **Public.** Open to the public, for any project to use.
