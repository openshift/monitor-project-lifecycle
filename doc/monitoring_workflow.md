# Monitoring Project Lifecycle Workflow

The purpose of this document is to describe the workflow that will be used to monitor the project lifecycle (new app, build app, deploy app, etc).

## Specific Steps

1. Project Name:

   The project name will be passed into the monitor-project-lifecycle app. This means that monitor-project-lifecycle will NOT generate a unique ID for the project name.

   Rough CLI equivalent:
   ```
   export project="os-monitor-project-lifecycle"
   ```

   NOTE: if it is desired for monitor-project-lifecycle to use a namespace that starts with openshift-\* or kube-\*, then the service account monitor-project-lifecycle will use MUST have rights to create projects with those prefixes.

1. Garbage collection:

   The first thing monitor-project-lifecycle will do is to check to see if a project it will create the app in exists. If it does, then it will delete it. Because of this, it is expected that monitor-project-lifecycle app will be run a single time with the same project name.

   Rough CLI equivalent:
   ```
   oc get project "$project" &> /dev/null && oc delete project "$project"
   ```

   This is essential in order to guarantee monitor-project-lifecycle is able to test deploying an application in a clean environment. Otherwise, cruft left around from previous, possibly failed runs may cause additional failed runs.

   Since we do this at the beginning of the monitor-project-lifecycle, it also means that we don't necessarily need to try to clean up the project at the end of our run. It may, however, be desirable to do this clean up both at the beginning as well as at the end of the monitored loop.

1. Create Project:

   Once the garbage from possible previous runs has been collected, we create a new clean project in which the monitored application will be deployed.

   Rough CLI equivalent:
   ```
   oc new-project "$project"
   ```

1. Create the monitored app:

   The application that will be built and deployed in order to gather metrics will come from one of the saved templates in the cluster. For these steps, the `django-psql-persistent` template was chosen. Any template may be chosen, but the specific resources deployed by the template (dc name, bc name, svc name, etc) MUST be specified.

   Rough CLI equivalent:
   ```
   oc new-app django-psql-persistent -n "$project"
   ```

1. Check on the status of the project (includes status of the deployments):

   Rough CLI equivalent:
   ```
   oc status -n "$project"
   ```

1. Check the build logs from the monitored app:

   Note: "Push successful" is the last line output in the build logs.

   Rough CLI equivalent:
   ```
   oc logs -f bc/django-psql-persistent -n "$project"
   ```

1. Check the container logs:

   Not sure if this is worthwhile, but might be a good check.
   Note: this log is of the active container, so the logs are never "finished" and thus we don't use the `-f` option.


   Rough CLI equivalent:
   ```
   oc logs dc/django-psql-persistent -n "$project"
   ```

1. Check that the DC thinks it's currently rolled out:

   Rough CLI equivalent:
   ```
   oc get dc -n "$project" django-psql-persistent
   ```

   Note: Output Looks like this:
   ```
   NAME                     REVISION   DESIRED   CURRENT   TRIGGERED BY
   django-psql-persistent   1          1         1         config,image(django-psql-persistent:latest)
   ```

1. Check that the service has been created:

   Rough CLI equivalent:
   ```
   oc get svc -n "$project" django-psql-persistent
   ```

1. Check that the cluster DNS for the service has been setup:

   Rough CLI equivalent:
   ```
   dig @127.0.0.1 -p 8053 django-psql-persistent.${project}.svc.cluster.local
   ```

1. Check that the liveness check reports healthy through the service:

   Check the liveness check using the service dns. Returns a string "0" if healthy

   NOTE: this doesn't work with cluster up and curl on the host because the host doesn't have access to the cluster DNS or the cluster SDN.

   Rough CLI equivalent:
   ```
   oc rsh -n "$project" dc/postgresql curl http://django-psql-persistent:8080/health
   ```

1. Make sure the route has been created:

   Rough CLI equivalent:
   ```
   oc get route -n "$project" django-psql-persistent
   ```

1. Get the route's DNS:

   Rough CLI equivalent:
   ```
   route_dns=$(oc get route -n "$project" --template="{{ .spec.host }}" django-psql-persistent)
   ```

1. Check that the liveness check reports healthy through the route:

   Check the liveness check using the route's dns. Returns a string "0" if healthy

   Rough CLI equivalent:
   ```
   curl http://${route_dns}/health
   ```

   NOTE: this doesn't currently work with cluster up and curl on my host. It just hangs. More work needs to be done.


1. Open Questions:
   * Should we cleanup the project on both the start of the loop and the end of the loop?
   * What rights are needed to create openshift-\* and kube-\* namespaces?
      * How do we grant those rights to the service account that monitor-project-lifecycle will use?
   * Should the stored template to use be a parameter to monitor-project-lifecycle?
      * Would be more flexible, but we'll also need to pass in the details of the deployed app. e.g. dc name, service name, route name, etc.
