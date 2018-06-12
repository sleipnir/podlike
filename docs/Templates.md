# Templates

Podlike comes with a built-in template processor, to help transforming services and their tasks in Docker Swarm stacks into a *"pod"*, a set of co-located and tightly coupled containers. Similar stacks, or similar types of applications in a stack, could often benefit from decorating the tasks with the same components, with only slightly different configuration. For example, [sidecars](https://github.com/rycus86/podlike/tree/master/examples/sidecar) or [service meshes](https://github.com/rycus86/podlike/tree/master/examples/service-mesh) usually need the same component consistently deployed with the applications themselves, and we'd probably want changes to these components done in a single place, and applied to all services (or a set of them) at the same time.

Using templates gives you a flexible way to define these components, and allows you to reuse the components accross stacks and services. Templates generate parts of a YAML [Compose file](https://docs.docker.com/compose/compose-file/) using Go's [text/template package](https://golang.org/pkg/text/template/). The templates to use are defined directly in the stack YAML files with extension fields, so the configuration lives within them, and can be versioned/changed/rolled out with the same workflows as the original stack.

## Configuration

The example YAML snippet below demonstrates the use of the Podlike-related [extension fields](https://docs.docker.com/compose/compose-file/#extension-fields) the template generator will look for.

```yaml
version: '3.5'

x-podlike:
  example:
    pod:
      - templates/pod.yml
      - http: https://templates.store.local/pods/example.yml
      - inline: |
          pod:
	    labels:
	      swarm.service.label: templated-for-{{ .Service.Name }}
    transformer:
      - templates/transformer.yml
      # or http or inline
    templates:
      - templates/components/sidecar.yml
      - templates/components/logger.yml
      - <<: *tracing-component
      - <<: *proxy-component
      # or http or inline
    copy:
      - inline: |
          sidecar: /var/conf/sidecar.conf:/etc/sidecar/conf.d/default.conf
      # or http or from file
    args:
      ExposedHttpPort: 8080
      ExtraEnvVars:
        - DEBUG=false
      # any other arguments you'd need available for the templates
  args:
    GlobalVariable: example
    # any other arguments you'd need available for the templates

services:

  example:
    image: example/app:0.1.2
    volumes:
      - log-data:/var/logs/example
    x-podlike:
      pod:
        # same as above
      transformer:
        # same as above
      templates:
        # same as above
      copy:
        # same as above
      args:
        # same as above

volumes:
  log-data:
    name: log-data-for-{{.Task.ID}}
    labels:
      com.github.rycus86.podlike.volume-ref: shared-log-folder

x-anchors:

  - &tracing-component
    templates/components/tracing.yml

  - &proxy-component
    templates/components/proxy.yml
```

Let's unpack the example above, and look at the different extension places.

## Top-level extension

Extension fields at the root level of a stack YAML are supported since Compose schema version `3.4`, and are simply ignored by a `docker stack deploy`. Podlike can use the `x-podlike` top-level extension field to define templates *per service*, matching the service name, plus any additional `args` to make available globally to the templates used within the stack.

For each service, we can define `pod` templates, `transformer` templates, `templates` for the additional components, `copy` configurations, and additional `args` available for templates used with this service. The additional arguments are merged with any global `args`.

```yaml
# top-level extension
x-podlike:
  svc1:
    pod:
    transformer:
    templates:
    copy:
    args:
  svc2:
    templates:
    args:
  args:
```

Every field is optional to use, and you can use a single template or a list of them for `pod`, `transformer`, `templates` or `copy`. If multiple templates are given for a single type, they will be merged together, in order - see more details below at [Template merging](#template-merging).

The example above would define templates to use on the `svc1` and `svc2` services, plus specific arguments for each service, as well as additional global arguments. See which template is used for what below, but first, let's have a quick overview of what types of parameters they accept.

### Template definition types

All 4 types of definitions accept either a single item, or a list of items. An item can be:

1. A simple string

This points to a template file.

```yaml
x-podlike:
  example:
    pod:
      - templates/pod.yml
```

2. An inline template mapping

This uses the given string as the template text.

```yaml
x-podlike:
  example:
    pod:
      - inline: |
          image: sample/{{ .Service.Name }}:{{ .Args.ImageTag }}
```

3. An HTTP(S) URL to the template

This fetches the template from the given URL, and uses the response content as the template text.

```yaml
x-podlike:
  example:
    pod:
      - http: https://my.templates.local/pods/sample.yml
```

### Controller templates

The templates listed under the `pod` key are used to construct the new Swarm service definition for the *controller*. This is allowed to produce a Swarm compatible service mapping, e.g. `deploy`, `configs`, `secrets`, etc. are OK.

If omitted, a default template is used to generate the `image` property pointing to `rycus86/podlike` with the same version as the template generator. The default also adds a volume mapping for the Docker engine socket at `/var/run/docker.sock` for convenience, and enables streaming logs from the components using the `-logs` Podlike flag.

If there is at least one template given, the template engine only makes sure there is an `image` defined, with the same rules as above, plus it adds volume for the Docker engine socket.

```yaml
x-podlike:
  example:
    pod:
      inline: |
        pod:
	  image: forked/podlike:{{ .Args.Version }}
	  deploy:
	    replicas: 3
    args:
      Version: 0.1.2
```

The name of the root property in the generated string doesn't matter, it will be replaced by the actual name of the service as given in the stack YAML. The template engine also copies over most of the properties from the original service definition, unless they are added by the templates, see these in the `mergedPodKeys` in the [merge.go](https://github.com/rycus86/podlike/blob/master/pkg/template/merge.go) source file.

Each of the templates given here must output a YAML compatible string with a single root property.

### Main component templates

The `transformer` templates generate the Compose-compatible *component* definition for the main component, that is the original image defined in the stack YAML in most cases, with its selected properties.

```yaml
x-podlike:
  example:
    transformer:
      inline: |
        main:
	  environment:
	    - EXTRA_VARS={{ .Args.ExtraEnv }}
	  {{ if .Service.ReadOnly }}
          read_only: true
	  {{ end }}
    args:
      ExtraEnv: some-env-var
```

It no templates given, a default one will copy over the `image` property from the original service definition, plus a fair bit of other properties are added automatically, defined by `mergedTransformerKeys` in the [merge.go](https://github.com/rycus86/podlike/blob/master/pkg/template/merge.go) source file.

Each of the templates given here must output a YAML compatible string with a single root property. Most of the [v2 Compose file](https://docs.docker.com/compose/compose-file/compose-file-v2/) properties are allowed, with the exceptions listed on the main project [README](https://github.com/rycus86/podlike/blob/master/README.md#unsupported-properties). The name of the result *component* will be the root key of the first template. Root keys defined by any other templates will be ignored, and converted automatically to the one defined by the first. The example above would use `main`, the default is `app`.

### Additional component templates

The templates listed under `templates` can define any number of *components*. These are meant to generate the Compose-compatible definitions of the containers to couple the main component with.

```yaml
x-podlike:
  example:
    templates:
      - templates/sidecar.yml
      - templates/service-discovery.yml
      - templates/tracing.yml
      - inline: |
          tracing:
	    mem_limit: 64m
      - inline: |
          tracing:
	    environment:
	      HTTP_PORT: {{ .Args.Tracing.Http.Port }}
    args:
      Tracing:
        Http:
	  Port: 12345
```

As with the other types, the templates are processed in the same order as they are defined in the YAML, and any common properties are merged in together. In the example above, the `templates/tracing.yml` template could define a component with the `tracing` name, then the last two templates would add in the `mem_limit` property, if not defined by the previous template already, plus the `environment` variables would also contain `HTTP_PORT`.

The names of the components come from root properties of the result YAML, after merging all the template outputs together.

### Copy templates

Podlike allows copying files from the *controller* container into the *component* containers before they start, and the `copy` templates can define the mappings for these.

```yaml
x-podlike:
  example:
    copy:
      - inline: |
          proxy: '/shared/proxy.conf:/var/conf/proxy/default.conf'
      - inline: |
          logging:
	    - /shared/logging.conf:/var/conf/logger/settings.properties
            - /shared/proxy.logging:/var/conf/logger/conf.d/proxy.conf
```

Each template needs to output a mapping of service name to copy configurations. The copy items will be converted into a `string` slice of `<source>:<target>` paths, but accepts a `<source>: <target>` mapping, or a single string as well. The lists generated by all the `copy` templates will be then merged into a single list, and put on the *controller* definition.

## Template merging

As mentioned above, each type of templates can use multiple source to generate the final markup, and they can output the same properties for the same component with different settings. Single-valued properties are going to be ignored if redefined, but *slices* and *maps* are merged together. A prime example of these would be `environment` variables or `labels`.

```yaml
x-podlike:
  example:
    transformer:
      - inline: |
          environment:
	    - HTTP_PROXY=my.local.proxy:8091
	  labels:
	    inline.label: sample
      - inline: |
          environment:
	    ADDED: 'new key, and is added'
	    HTTP_PROXY: 'ignored as already defined'
            # note that `- HTTP_PROXY=override` would have been added
            # because at this point the template engine wouldn't assume it's
            # a key-value pair as a string, only when it sees that it can be a mapping
	  labels:
	    inline.label:      ignored
	    additional.label:  added
```

The merging logic works on a best-effort basis to merge items of the same property together, even if they are of different types. It can:

- Merge items of a *map* into another *map*
- Merge items of a *slice* of `key=value` pairs into a *map*
- Merge items of a *slice* into another *slice*
- Merge items of a *map* into a *slice* after converting it to a *map* as `key=value` pairs
- Add a *string* into a *slice*

See the implementation in the [merge.go](https://github.com/rycus86/podlike/blob/master/pkg/template/merge.go) file, and also the tests for these cases in the [merge_test.go](https://github.com/rycus86/podlike/blob/master/pkg/template/merge_test.go) file.

## Service-level extension

> TODO

## Using YAML anchors

> TODO

## Template variables

> TODO

## Usage

> TODO with docker
> TODO with the script
