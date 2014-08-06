package main

import (
	"encoding/xml"
	"fmt"
	"github.com/alexzorin/libvirt-go"
	"github.com/gin-gonic/gin"
	"runtime"
	"strings"
)

var stateDict = map[uint8]string{
	libvirt.VIR_DOMAIN_NOSTATE:     "nostate",
	libvirt.VIR_DOMAIN_RUNNING:     "running",
	libvirt.VIR_DOMAIN_BLOCKED:     "blocked",
	libvirt.VIR_DOMAIN_PAUSED:      "paused",
	libvirt.VIR_DOMAIN_SHUTDOWN:    "shutdown",
	libvirt.VIR_DOMAIN_CRASHED:     "crashed",
	libvirt.VIR_DOMAIN_PMSUSPENDED: "suspended",
	libvirt.VIR_DOMAIN_SHUTOFF:     "shutoff",
}

type (
	DiskDriver struct {
		Name string `xml:"name,attr" json:"name"`
		Type string `xml:"type,attr" json:"type"`
	}

	DiskSource struct {
		File   string `xml:"file,omitempty,attr" json:"file,omitempty"`
		Device string `xml:"dev,omitempty,attr" json:"device,omitempty"`
	}

	DiskTarget struct {
		Dev string `xml:"dev,attr" json:"dev"`
		Bus string `xml:"bus,attr" json:"bus"`
	}

	Disk struct {
		Type   string     `xml:"type,attr"  json:"type"`
		Device string     `xml:"device,attr"  json:"device"`
		Driver DiskDriver `xml:"driver"  json:"driver"`
		Source DiskSource `xml:"source" json:"source"`
		Target DiskTarget `xml:"target" json:"target"`
	}

	InterfaceSource struct {
		Network string `xml:"network,omitempty,attr" json:"network,omitempty"`
		Bridge  string `xml:"bridge,omitempty,attr" json:"bridge,omitempty"`
	}

	InterfaceMac struct {
		Address string `xml:"address,attr" json:"address"`
	}

	InterfaceModel struct {
		Type string `xml:"type,omitempty,attr" json:"type,omitempty"`
	}

	FilterRefParameter struct {
		Name  string `xml:"name,attr" json:"name"`
		Value string `xml:"value,attr" json:"value"`
	}

	FilterRef struct {
		Filter     string               `xml:"filter,attr"  json:"filter"`
		Parameters []FilterRefParameter `xml:"parameter" json:"parameters"`
	}

	Interface struct {
		Type      string          `xml:"type,attr"  json:"type"`
		Source    InterfaceSource `xml:"source,omitempty" json:"source,omitempty"`
		Mac       InterfaceMac    `xml:"mac,omitempty" json:"mac,omitempty"`
		Model     InterfaceModel  `xml:"model,omitempty" json:"model,omitempty"`
		FilterRef FilterRef       `xml:"filterref,omitempty" json:"filterref,omitempty"`
	}

	Device struct {
		Disks      []Disk      `xml:"disk" json:"disks"`
		Interfaces []Interface `xml:"interface" json:"interfaces"`
	}

	OsType struct {
		Type    string `xml:",chardata" json:"type,omitempty"`
		Arch    string `xml:"arch,attr,omitempty" json:"arch,omitempty"`
		Machine string `xml:"machine,attr,omitempty" json:"machine,omitempty"`
	}

	OsBoot struct {
		Dev string `xml:"dev,attr,omitempty" json:"dev,omitempty"`
	}

	Os struct {
		Type OsType `xml:"type,omitempty" json:"type,omitempty"`
		Boot OsBoot `xml:"boot,omitempty" json:"boot,omitempty"`
	}

	Domain struct {
		*libvirt.VirDomain `xml:"-" json:"-"`
		XMLName            struct{} `xml:"domain" json:"-"`
		Type               string   `xml:"type,attr" json:"type"`
		UUID               string   `xml:"uuid" json:"uuid"`
		Name               string   `xml:"name" json:"name"`
		Memory             int      `xml:"memory" json:"memory"`
		VCPU               int      `xml:"vcpu" json:"vpcu"`
		Devices            Device   `xml:"devices,omitempty" json:"devices"`
		Os                 Os       `xml:"os,omitempty" json:"os"`
		domain             *libvirt.VirDomain
		State              string `xml:"-" json:"state"`
	}

	ErrorMsg struct {
		Error string `json:"error"`
	}

	Context struct {
		*gin.Context
		V        *libvirt.VirConnection
		freelist []Freer
	}

	HandlerFunc func(*Context) error

	Freer interface {
		Free()
	}
)

func (d *Domain) Free() {
	if d.VirDomain != nil {
		fmt.Printf("Freeing: %s\n", d.Name)
		d.VirDomain.Free()
		d.VirDomain = nil
	}
}

func (c *Context) FreeList(f Freer) {
	c.freelist = append(c.freelist, f)
}

func buildDomain(dom *libvirt.VirDomain) (*Domain, error) {
	d := new(Domain)
	d.VirDomain = dom

	xmldesc, err := d.GetXMLDesc(0)
	if err != nil {
		return nil, err
	}
	err = xml.Unmarshal([]byte(xmldesc), d)
	if err != nil {
		return nil, err
	}

	state, err := d.GetState()
	if err != nil {
		return nil, err
	}

	d.State = stateDict[uint8(state[0])]

	runtime.SetFinalizer(d, func(d *Domain) {
		d.Free()
	})
	return d, nil
}

func listDomains(c *Context) error {
	doms, err := c.V.ListAllDomains(0)
	if err != nil {
		return c.JSONError(500, err)
	}
	domains := make([]*Domain, len(doms))
	for i, _ := range doms {
		d, err := buildDomain(&doms[i])
		if err != nil {
			return c.JSONError(500, err)
		}
		c.FreeList(d)
		domains[i] = d
	}
	c.JSON(200, domains)
	return nil
}

func getDomain(c *Context, d *Domain) error {
	c.JSON(200, d)
	return nil
}

func domainAction(action string) gin.HandlerFunc {
	return domainHandler(func(c *Context, d *Domain) error {
		var err error
		switch action {

		case "destroy":
			err = d.Destroy()

		case "create":
			err = d.Create()

		case "reboot":
			err = d.Reboot(0)

		case "resume":
			err = d.Resume()

		case "suspend":
			err = d.Suspend()

		case "shutdown":
			err = d.Shutdown()

		}
		if err != nil {
			return c.JSONError(500, err)
		}
		c.JSON(200, d)
		return nil
	})
}

func domainHandler(fn func(*Context, *Domain) error) gin.HandlerFunc {
	return gin.HandlerFunc(func(c *gin.Context) {
		v, err := libvirt.NewVirConnection("qemu:///system")
		if err != nil {
			c.Abort(500)
		}
		defer v.CloseConnection()
		ctx := &Context{
			Context:  c,
			V:        &v,
			freelist: make([]Freer, 0),
		}
		defer func() {
			for _, f := range ctx.freelist {
				f.Free()
			}
		}()
		name := c.Params.ByName("name")
		dom, err := v.LookupDomainByName(name)

		if err != nil {
			code := 500
			if strings.Contains(err.Error(), "Domain not found") {
				code = 404
			}
			ctx.JSONError(code, err)
			return
		}
		d, err := buildDomain(&dom)
		if err != nil {
			ctx.JSONError(500, err)
			return
		}
		defer d.Free()
		fn(ctx, d)
	})
}

func withContext(fn HandlerFunc) gin.HandlerFunc {
	return gin.HandlerFunc(func(c *gin.Context) {
		v, err := libvirt.NewVirConnection("qemu:///system")
		if err != nil {
			c.Abort(500)
		}
		defer v.CloseConnection()

		ctx := &Context{
			Context:  c,
			V:        &v,
			freelist: make([]Freer, 0),
		}
		defer func() {
			for _, f := range ctx.freelist {
				f.Free()
			}
		}()
		fn(ctx)
	})
}

func (c *Context) JSONError(code int, err error) error {
	c.JSON(code, ErrorMsg{Error: err.Error()})
	return err
}

func main() {
	r := gin.Default()
	r.GET("/ping", func(c *gin.Context) {
		c.String(200, "pong")
	})

	domains := r.Group("/domains")
	{
		domains.GET("", withContext(listDomains))
		domains.GET(":name", domainHandler(getDomain))

		for _, action := range []string{"destroy", "create", "reboot", "resume", "suspend", "shutdown"} {
			domains.POST(fmt.Sprintf(":name/%s", action), domainAction(action))
		}
	}

	// Listen and server on 0.0.0.0:8080
	r.Run(":8080")
}
