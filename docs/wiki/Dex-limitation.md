Though Dex can be used for a very wide range of use case even just CRUD, Dex is NOT for everything. It is not suitable for use cases like:

* High performance transaction( e.g. within 10ms)
* High frequent writes on a single workflow execution(like a single record in database) for hot partition issue
* High frequent reads on a single workflow execution is okay if using memo for data attributes
* Join operation across different workflows
* Transaction for operation across multiple workflows
