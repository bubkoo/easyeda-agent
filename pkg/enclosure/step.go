package enclosure

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// STEP encodes the part as an ISO 10303-21 file (AP214, automotive_design)
// holding one FACETED_BREP: every merged planar face becomes a FACE_SURFACE on
// a PLANE bounded by a POLY_LOOP. Units are millimetres.
func (p Part) STEP(name string) ([]byte, error) {
	if p.mesh == nil || len(p.mesh.faces) == 0 {
		return nil, fmt.Errorf("part %s has no mesh", p.Name)
	}
	var b strings.Builder
	id := 0
	ent := func(format string, a ...any) int {
		id++
		fmt.Fprintf(&b, "#%d=%s;\n", id, fmt.Sprintf(format, a...))
		return id
	}
	num := func(v float64) string {
		s := strconv.FormatFloat(v, 'f', -1, 64)
		if !strings.ContainsAny(s, ".e") {
			s += "."
		}
		return s
	}
	vecs := func(v vec) string { return num(v[0]) + "," + num(v[1]) + "," + num(v[2]) }
	label := strings.ReplaceAll(name, "'", "")
	fmt.Fprintf(&b, "ISO-10303-21;\nHEADER;\nFILE_DESCRIPTION(('easyeda-agent enclosure %s'),'2;1');\n", label)
	fmt.Fprintf(&b, "FILE_NAME('%s.step','',(''),(''),'easyeda-agent','easyeda-agent','');\n", label)
	b.WriteString("FILE_SCHEMA(('AUTOMOTIVE_DESIGN { 1 0 10303 214 1 1 1 1 }'));\nENDSEC;\nDATA;\n")
	appCtx := ent("APPLICATION_CONTEXT('automotive design')")
	ent("APPLICATION_PROTOCOL_DEFINITION('international standard','automotive_design',2000,#%d)", appCtx)
	prodCtx := ent("PRODUCT_CONTEXT('',#%d,'mechanical')", appCtx)
	prod := ent("PRODUCT('%s','%s','',(#%d))", label, label, prodCtx)
	ent("PRODUCT_RELATED_PRODUCT_CATEGORY('part',$,(#%d))", prod)
	form := ent("PRODUCT_DEFINITION_FORMATION('','',#%d)", prod)
	defCtx := ent("PRODUCT_DEFINITION_CONTEXT('part definition',#%d,'design')", appCtx)
	def := ent("PRODUCT_DEFINITION('design','',#%d,#%d)", form, defCtx)
	shapeDef := ent("PRODUCT_DEFINITION_SHAPE('','',#%d)", def)
	length := ent("(LENGTH_UNIT()NAMED_UNIT(*)SI_UNIT(.MILLI.,.METRE.))")
	angle := ent("(NAMED_UNIT(*)PLANE_ANGLE_UNIT()SI_UNIT($,.RADIAN.))")
	solidAngle := ent("(NAMED_UNIT(*)SI_UNIT($,.STERADIAN.)SOLID_ANGLE_UNIT())")
	unc := ent("UNCERTAINTY_MEASURE_WITH_UNIT(LENGTH_MEASURE(1.E-06),#%d,'distance_accuracy_value','')", length)
	geoCtx := ent("(GEOMETRIC_REPRESENTATION_CONTEXT(3)GLOBAL_UNCERTAINTY_ASSIGNED_CONTEXT((#%d))GLOBAL_UNIT_ASSIGNED_CONTEXT((#%d,#%d,#%d))REPRESENTATION_CONTEXT('',''))", unc, length, angle, solidAngle)
	origin := ent("CARTESIAN_POINT('',(0.,0.,0.))")
	axis := ent("AXIS2_PLACEMENT_3D('',#%d,$,$)", origin)

	points := map[int]int{}
	point := func(i int) int {
		if e, ok := points[i]; ok {
			return e
		}
		e := ent("CARTESIAN_POINT('',(%s))", vecs(p.mesh.pos[i]))
		points[i] = e
		return e
	}
	faces := make([]string, 0, len(p.mesh.faces))
	for _, f := range p.mesh.faces {
		refs := make([]string, len(f.ring))
		for i, v := range f.ring {
			refs[i] = "#" + strconv.Itoa(point(v))
		}
		loop := ent("POLY_LOOP('',(%s))", strings.Join(refs, ","))
		bound := ent("FACE_OUTER_BOUND('',#%d,.T.)", loop)
		loc := point(f.ring[0])
		normal := ent("DIRECTION('',(%s))", vecs(f.normal))
		ref := ent("DIRECTION('',(%s))", vecs(perpendicular(f.normal)))
		plane := ent("PLANE('',#%d)", ent("AXIS2_PLACEMENT_3D('',#%d,#%d,#%d)", loc, normal, ref))
		faces = append(faces, "#"+strconv.Itoa(ent("FACE_SURFACE('',(#%d),#%d,.T.)", bound, plane)))
	}
	shell := ent("CLOSED_SHELL('',(%s))", strings.Join(faces, ","))
	brep := ent("FACETED_BREP('%s',#%d)", label, shell)
	rep := ent("FACETED_BREP_SHAPE_REPRESENTATION('%s',(#%d,#%d),#%d)", label, axis, brep, geoCtx)
	ent("SHAPE_DEFINITION_REPRESENTATION(#%d,#%d)", shapeDef, rep)
	b.WriteString("ENDSEC;\nEND-ISO-10303-21;\n")
	return []byte(b.String()), nil
}

// perpendicular returns a unit vector orthogonal to an axis-aligned normal.
func perpendicular(n vec) vec {
	if math.Abs(n[0]) < 0.9 {
		return vec{1, 0, 0}
	}
	return vec{0, 1, 0}
}
